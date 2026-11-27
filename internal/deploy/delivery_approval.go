package deploy

import (
	"fmt"
	"strings"
	"time"
)

type ApprovalDecision struct {
	WorkItemIDs []int64 `json:"work_item_ids,omitempty"`
	Actor       string  `json:"actor"`
	Approved    bool    `json:"approved"`
	Comment     string  `json:"comment,omitempty"`
	At          int64   `json:"at"`
}

func hasApprovalNodes(p *DeliveryPlan) bool {
	for _, n := range p.Nodes {
		if n.Kind == "approval" {
			return true
		}
	}
	return false
}
func approvalNode(p *DeliveryPlan, id string) *DeliveryNode {
	for i := range p.Nodes {
		if p.Nodes[i].ID == id && p.Nodes[i].Kind == "approval" {
			return &p.Nodes[i]
		}
	}
	return nil
}
func namedApprover(n *DeliveryNode, actor string) bool {
	for _, a := range n.Approvers {
		if a == actor {
			return true
		}
	}
	return false
}
func validateApprovalNode(n DeliveryNode) error {
	if n.ApprovalMode != "any" && n.ApprovalMode != "all" {
		return fmt.Errorf("审核节点 %s 须选择或签或会签", n.Name)
	}
	if len(n.Approvers) < 1 || len(n.Approvers) > 25 {
		return fmt.Errorf("审核节点 %s 需要 1–25 名审批人", n.Name)
	}
	seen := map[string]bool{}
	for _, a := range n.Approvers {
		if strings.TrimSpace(a) != a || a == "" || a == "local" || seen[a] {
			return fmt.Errorf("审核人员不能为空、重复或为未登录身份")
		}
		seen[a] = true
	}
	return nil
}
func validateRunApprovals(p *DeliveryPlan, owner string) error {
	if owner == "local" && hasApprovalNodes(p) {
		return fmt.Errorf("人工审核需要先启用平台登录，不能以本地匿名身份提交")
	}
	for _, n := range p.Nodes {
		if n.Kind != "approval" {
			continue
		}
		eligible := 0
		for _, a := range n.Approvers {
			if deliveryReviewRequired(p) && !allowedApprover(p.Environment.Approvers, a) {
				return fmt.Errorf("审核节点 %s 的 %s 不在环境审批名单中", n.Name, a)
			}
			if a == owner {
				if n.ApprovalMode == "all" {
					return fmt.Errorf("会签节点 %s 包含发起人，无法自审", n.Name)
				}
			} else {
				eligible++
			}
		}
		if eligible == 0 {
			return fmt.Errorf("审核节点 %s 缺少发起人之外的审批人", n.Name)
		}
	}
	return nil
}

// Decisions are part of the immutable run's execution document and use the
// same revision CAS as execution. An administrator cannot skip a stage.
func (s *Store) ReviewDeliveryNode(id int64, actor, digest, nodeID, comment string, approve bool) (*DeliveryRun, error) {
	v := s.DeliveryRun(id)
	if v != nil && v.Plan.Template.Workbench == "research" {
		return nil, fmt.Errorf("请通过研发工作台处理版本准备审批")
	}
	if v == nil || v.Status != "pending_approval" || v.Digest != digest {
		return nil, fmt.Errorf("审核单状态或内容摘要已改变")
	}
	if actor == v.Owner {
		return nil, fmt.Errorf("发起人不能审批自己的发布")
	}
	if err := s.checkDeliveryPolicy(&v.Plan); err != nil {
		return nil, err
	}
	env := s.GetEnvironment(v.Plan.Environment.Name)
	if env == nil {
		return nil, fmt.Errorf("发布环境不存在")
	}
	var step *DeliveryStep
	for i := range v.Steps {
		if v.Steps[i].Kind == "approval" && v.Steps[i].Status != "success" {
			step = &v.Steps[i]
			break
		}
	}
	if step == nil || step.NodeID != nodeID {
		return nil, fmt.Errorf("只能审核当前阶段，不能提前审核或重放旧节点")
	}
	n := approvalNode(&v.Plan, nodeID)
	if n == nil || !namedApprover(n, actor) || ((env.Protected || deliveryReviewRequired(&v.Plan)) && (!allowedApprover(env.Approvers, actor) || !allowedApprover(v.Plan.Environment.Approvers, actor))) {
		return nil, fmt.Errorf("仅当前节点指定且仍有权限的审批人可以审核")
	}
	comment = strings.TrimSpace(comment)
	if len(comment) > 8000 || (!approve && comment == "" && (env.Protected || v.Plan.Environment.Protected)) {
		return nil, fmt.Errorf("驳回须填写原因，审核意见不得超过 8000 字节")
	}
	for _, r := range step.Reviews {
		if r.Actor == actor {
			return nil, fmt.Errorf("本阶段已签署，请等待其他审批人")
		}
	}
	now := time.Now().Unix()
	if step.StartedAt == 0 {
		step.StartedAt = now
	}
	step.Reviews = append(step.Reviews, ApprovalDecision{Actor: actor, Approved: approve, Comment: comment, At: now})
	step.Status = "reviewing"
	if !approve {
		step.Status = "rejected"
		step.EndedAt = now
		v.Status = "rejected"
	} else if n.ApprovalMode == "any" || len(step.Reviews) == len(n.Approvers) {
		step.Status = "success"
		step.EndedAt = now
		complete := true
		for _, next := range v.Steps {
			if next.Kind == "approval" && next.Status != "success" {
				complete = false
			}
		}
		if complete {
			v.Status = "approved"
			v.Approver = actor
		}
	}
	if err := s.saveDeliveryRun(v); err != nil {
		return nil, err
	}
	s.Audit(actor, "review-node", "delivery-run", fmt.Sprint(id), map[string]any{"node": nodeID, "approved": approve, "comment": comment, "digest": digest})
	return v, nil
}
func (s *Store) checkRunApprovals(v *DeliveryRun, env *Environment) error {
	for _, step := range v.Steps {
		if step.Kind != "approval" {
			continue
		}
		n := approvalNode(&v.Plan, step.NodeID)
		if n == nil || step.Status != "success" {
			return fmt.Errorf("尚有审核节点未通过，禁止执行")
		}
		seen := map[string]bool{}
		for _, r := range step.Reviews {
			if !r.Approved || r.Actor == v.Owner || !namedApprover(n, r.Actor) || seen[r.Actor] || ((env.Protected || deliveryReviewRequired(&v.Plan)) && !allowedApprover(env.Approvers, r.Actor)) {
				return fmt.Errorf("审核人员或审批权限已变化，请重新提交")
			}
			seen[r.Actor] = true
		}
		if len(seen) == 0 || (n.ApprovalMode == "all" && len(seen) != len(n.Approvers)) {
			return fmt.Errorf("或签/会签条件未满足")
		}
	}
	return nil
}
