package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

var errXXLJobUncertain = errors.New("原执行回执待核对，平台不会自动重复定时任务写入")

type XXLJobPlan struct {
	NodeID         string      `json:"node_id"`
	ChangeID       int64       `json:"change_id"`
	Service        string      `json:"service"`
	Environment    string      `json:"environment"`
	Alias          string      `json:"alias"`
	Connection     string      `json:"connection"`
	GroupID        int         `json:"group_id"`
	AppName        string      `json:"app_name"`
	BindingDigest  string      `json:"binding_digest"`
	Action         string      `json:"action"`
	Before         *XXLJobTask `json:"before,omitempty"`
	After          *XXLJobTask `json:"after,omitempty"`
	CreatedBy      int64       `json:"created_by_change_id,omitempty"`
	Verification   string      `json:"verification_node,omitempty"`
	CreationNode   string      `json:"creation_node,omitempty"`
	DeploymentNode string      `json:"deployment_node,omitempty"`
}

type xxlJobEffect struct {
	RunID     int64      `json:"run_id"`
	Digest    string     `json:"digest"`
	Operation string     `json:"operation"`
	Task      XXLJobTask `json:"task"`
}

func xxlNodeID(action string, changeID int64) string {
	return standardNodeID("xxl-job", action, fmt.Sprint(changeID))
}

func includeXXLJobNodes(nodes []DeliveryNode, scope *DeliveryScope) ([]DeliveryNode, error) {
	if scope == nil {
		return nodes, nil
	}
	changes := map[int64]Change{}
	for _, change := range scope.Changes {
		changes[change.ID] = change
	}
	prefixes := map[string][]DeliveryNode{}
	prefixed := map[int64]bool{}
	enabled := map[int64]bool{}
	writes := map[int64]DeliveryNode{}
	verifications := map[int64]DeliveryNode{}
	for _, change := range scope.Changes {
		if change.Kind != xxlJobKind {
			continue
		}
		if change.XXLJob == nil {
			return nil, fmt.Errorf("定时任务变更 #%d 内容不完整", change.ID)
		}
		job := change.XXLJob
		label := map[string]string{"create": "创建任务（停用）", "update": "修改任务", "enable": "启用任务", "disable": "停用任务"}[job.Action]
		write := DeliveryNode{ID: xxlNodeID(job.Action, change.ID), Kind: xxlJobKind, Service: change.Service,
			Name: label + " · " + change.Title, ChangeIDs: []int64{change.ID}}
		if job.CreatedByChangeID > 0 {
			creation := changes[job.CreatedByChangeID]
			if creation.XXLJob == nil || creation.XXLJob.Action != "create" ||
				creation.XXLJob.ResourceAlias != job.ResourceAlias || creation.Service != change.Service {
				return nil, fmt.Errorf("启用变更 #%d 必须引用本次范围内同执行器的创建变更", change.ID)
			}
			write.DependsOn = append(write.DependsOn, xxlNodeID("create", creation.ID))
			if enabled[creation.ID] {
				return nil, fmt.Errorf("创建变更 #%d 已关联一个启用步骤", creation.ID)
			}
			enabled[creation.ID] = true
			deployID := ""
			for _, dep := range change.Prerequisites {
				code := changes[dep.ChangeID]
				if code.Kind != "code" && code.Kind != "artifact" || code.Service != change.Service {
					continue
				}
				for _, item := range nodes {
					if changeNodeMatches(item, code) {
						if deployID != "" && deployID != item.ID {
							return nil, fmt.Errorf("启用变更 #%d 对应多个应用发布节点", change.ID)
						}
						deployID = item.ID
					}
				}
			}
			if deployID != "" {
				prefixed[creation.ID] = true
				prefixes[deployID] = append(prefixes[deployID], DeliveryNode{ID: xxlNodeID("create", creation.ID)})
			}
			verify := DeliveryNode{ID: xxlNodeID("verify", change.ID), Kind: "manual", Service: change.Service, XXLJobVerification: true,
				Name: "验证执行器就绪 · " + change.Title, DependsOn: []string{xxlNodeID("create", creation.ID)},
				Instructions: "核对本环境执行器实例、实际运行版本及任务 Handler。记录核对的版本或实例和验证结果后继续。"}
			if deployID != "" {
				verify.DependsOn = append(verify.DependsOn, deployID)
			}
			verifications[change.ID] = verify
			write.DependsOn = append(write.DependsOn, verify.ID)
		}
		writes[change.ID] = write
	}
	if len(writes) == 0 {
		return nodes, nil
	}
	byNodeID := map[string]DeliveryNode{}
	for _, write := range writes {
		byNodeID[write.ID] = write
	}
	result := make([]DeliveryNode, 0, len(nodes)+len(writes)+len(verifications))
	for _, node := range nodes {
		if node.Kind == xxlJobKind {
			return nil, fmt.Errorf("定时任务步骤从所选变更生成，请移除模板中的手工定时任务节点")
		}
		node.DependsOn = slices.Clone(node.DependsOn)
		for _, prefix := range prefixes[node.ID] {
			write := byNodeID[prefix.ID]
			write.DependsOn = slices.Clone(node.DependsOn)
			result = append(result, write)
			node.DependsOn = append(node.DependsOn, write.ID)
		}
		result = append(result, node)
	}
	for _, change := range scope.Changes {
		if write, ok := writes[change.ID]; ok && !prefixed[change.ID] {
			if _, hasVerification := verifications[change.ID]; !hasVerification {
				result = append(result, write)
			}
		}
	}
	for _, change := range scope.Changes {
		if verification, ok := verifications[change.ID]; ok {
			result = append(result, verification, writes[change.ID])
		}
	}
	return result, nil
}

func validateXXLOverrides(expected, actual []DeliveryNode) error {
	for _, node := range actual {
		if node.Kind == xxlJobKind || node.XXLJobVerification {
			original := nodeByID(expected, node.ID)
			if original == nil || original.Kind != node.Kind || original.XXLJobVerification != node.XXLJobVerification {
				return fmt.Errorf("不能添加范围外的定时任务节点")
			}
		}
	}
	for _, node := range expected {
		if node.Kind != xxlJobKind && !node.XXLJobVerification {
			continue
		}
		match := nodeByID(actual, node.ID)
		if match == nil || digestJSON(node) != digestJSON(*match) {
			return fmt.Errorf("定时任务节点 %s 需随交付范围更新", node.Name)
		}
		if node.XXLJobVerification {
			for _, id := range node.DependsOn {
				deploy := nodeByID(expected, id)
				if deploy == nil || deploy.Kind != "deploy" {
					continue
				}
				current := nodeByID(actual, id)
				if current == nil || current.Kind != deploy.Kind || current.Service != deploy.Service ||
					!slices.ContainsFunc(current.DependsOn, func(dep string) bool {
						return slices.Contains(node.DependsOn, dep) && dep != id
					}) {
					return fmt.Errorf("关联的应用发布节点须保留本次任务创建依赖：%s", deploy.Name)
				}
			}
		}
	}
	return nil
}

func (s *Store) xxlBindingDigest(binding xxlJobBinding, group XXLJobGroup) string {
	var configured []string
	if group.AddressType == 1 {
		configured = group.RegistryList
	}
	return s.privateResourceDigest([]any{s.connectionCheckFingerprint(binding.Connection), group.ID, group.AppName, group.AddressType, configured})
}

func xxlEffectKey(changeID int64, environment string) string {
	return fmt.Sprintf("xxl-job-effect:%d:%s", changeID, environment)
}

func (s *Store) xxlEffect(changeID int64, environment string) (*xxlJobEffect, error) {
	var raw string
	if err := s.db.QueryRow("SELECT value FROM deploy_meta WHERE key=?", xxlEffectKey(changeID, environment)).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var receipt xxlJobEffect
	err := json.Unmarshal([]byte(raw), &receipt)
	return &receipt, err
}

func (s *Store) prepareXXLJob(ctx context.Context, plan *DeliveryPlan, node DeliveryNode) (*XXLJobPlan, error) {
	if plan.Scope == nil || len(node.ChangeIDs) != 1 {
		return nil, fmt.Errorf("定时任务节点必须绑定一项明确选择的变更")
	}
	changeID := node.ChangeIDs[0]
	index := slices.IndexFunc(plan.Scope.Changes, func(change Change) bool {
		return change.ID == changeID && change.Kind == xxlJobKind && change.Service == node.Service
	})
	if index < 0 {
		return nil, fmt.Errorf("定时任务变更 #%d 未选入交付范围", changeID)
	}
	change := plan.Scope.Changes[index]
	if change.Status != ChangeReady {
		return nil, fmt.Errorf("定时任务变更 #%d 尚未就绪", changeID)
	}
	if err := normalizeXXLJobChange(&change); err != nil {
		return nil, err
	}
	environment := plan.Environment.Name
	if previous, err := s.xxlEffect(changeID, environment); err != nil {
		return nil, err
	} else if previous != nil {
		return nil, fmt.Errorf("变更 #%d 在此环境已有交付回执，请核对原执行单 #%d", changeID, previous.RunID)
	}
	job := change.XXLJob
	binding, err := s.xxlJobBinding(change.Service, environment, job.ResourceAlias)
	if err != nil {
		return nil, err
	}
	group, err := binding.group(ctx)
	if err != nil {
		return nil, err
	}
	frozen := &XXLJobPlan{
		NodeID: node.ID, ChangeID: change.ID, Service: change.Service, Environment: environment, Alias: job.ResourceAlias,
		Connection: binding.Connection.Name, GroupID: group.ID, AppName: group.AppName, BindingDigest: s.xxlBindingDigest(binding, group),
		Action: job.Action, CreatedBy: job.CreatedByChangeID,
	}
	if job.CreatedByChangeID > 0 {
		for _, previous := range plan.XXLJobPlans {
			if previous.ChangeID == job.CreatedByChangeID && previous.Action == "create" &&
				previous.BindingDigest == frozen.BindingDigest && previous.After != nil && previous.After.ScheduleType != "NONE" {
				frozen.CreationNode, frozen.Verification = previous.NodeID, xxlNodeID("verify", change.ID)
				verify := nodeByID(plan.Nodes, frozen.Verification)
				if verify != nil {
					for _, dep := range verify.DependsOn {
						if target := nodeByID(plan.Nodes, dep); target != nil && target.Kind == "deploy" {
							frozen.DeploymentNode = target.ID
						}
					}
				}
				frozen.After = previous.After
				return frozen, nil
			}
		}
		return nil, fmt.Errorf("启用步骤缺少本次创建任务的预检，任务须配置可启用的调度规则")
	}
	input := xxlGroupInput(group)
	if job.Action != "create" {
		input["target_title"], input["target_handler"] = job.TargetTitle, job.TargetHandler
		var matches []XXLJobTask
		if err := xxlCall(ctx, binding.Connection, "resolve", input, &matches); err != nil {
			return nil, err
		}
		if len(matches) != 1 || matches[0].ID < 1 || matches[0].GroupID != group.ID || matches[0].Revision == "" {
			return nil, fmt.Errorf("标题和 Handler 没有唯一匹配目标任务，请在目标环境核对")
		}
		frozen.Before = &matches[0]
		input["task_id"], input["revision"] = frozen.Before.ID, frozen.Before.Revision
	}
	input["action"], input["config"] = "validate-"+job.Action, job.Config
	var preview XXLJobTask
	if err := xxlCall(ctx, binding.Connection, "apply", input, &preview); err != nil {
		return nil, err
	}
	if preview.GroupID != group.ID || preview.Revision == "" {
		return nil, fmt.Errorf("调度中心返回的预检目标无效")
	}
	switch job.Action {
	case "enable":
		preview.TriggerStatus = 1
	case "disable":
		preview.TriggerStatus = 0
	}
	frozen.After = &preview
	return frozen, nil
}

func (s *Store) executeXXLJob(ctx context.Context, run *DeliveryRun, plan *XXLJobPlan) (string, error) {
	change := s.GetChange(plan.ChangeID)
	if change == nil || change.XXLJob == nil || change.Status != ChangeReady || run.Plan.Scope == nil ||
		!slices.ContainsFunc(run.Plan.Scope.Changes, func(item Change) bool {
			return item.ID == change.ID && digestJSON(item.XXLJob) == digestJSON(change.XXLJob)
		}) {
		return "", fmt.Errorf("定时任务变更内容已变化，请重新预检")
	}
	binding, err := s.xxlJobBinding(plan.Service, plan.Environment, plan.Alias)
	if err != nil {
		return "", err
	}
	group, err := binding.group(ctx)
	if err != nil {
		return "", err
	}
	if s.xxlBindingDigest(binding, group) != plan.BindingDigest {
		return "", fmt.Errorf("已审核的 XXL-JOB 连接或执行器已变化，请重新预检")
	}
	input := xxlGroupInput(group)
	input["action"], input["config"] = plan.Action, change.XXLJob.Config
	if plan.CreatedBy > 0 {
		creation, err := s.xxlEffect(plan.CreatedBy, plan.Environment)
		if err != nil {
			return "", err
		}
		if creation == nil || creation.RunID != run.ID || creation.Task.ID <= 0 || creation.Task.GroupID != group.ID ||
			!slices.ContainsFunc(run.Steps, func(step DeliveryStep) bool { return step.NodeID == plan.CreationNode && step.Status == "success" }) ||
			!slices.ContainsFunc(run.Steps, func(step DeliveryStep) bool {
				return step.NodeID == plan.Verification && step.Status == "success" && len(step.Reviews) > 0
			}) {
			return "", fmt.Errorf("缺少本次创建回执或执行器验证结果，未启用任务")
		}
		if plan.DeploymentNode != "" && !slices.ContainsFunc(run.Steps, func(step DeliveryStep) bool {
			return step.NodeID == plan.DeploymentNode && step.Service == plan.Service && step.Status == "success" && step.ReleaseID > 0 && step.ManualRecovery == nil
		}) {
			return "", fmt.Errorf("当前应用尚无本次发布成功回执，未启用任务")
		}
		if len(group.RegistryList) == 0 {
			return "", fmt.Errorf("执行器尚无可用注册或配置地址，未启用任务")
		}
		input["task_id"], input["revision"] = creation.Task.ID, creation.Task.Revision
	} else if plan.Before != nil {
		input["task_id"], input["revision"] = plan.Before.ID, plan.Before.Revision
	}
	key := xxlEffectKey(plan.ChangeID, plan.Environment)
	receipt := xxlJobEffect{RunID: run.ID, Digest: digestJSON(plan), Operation: randomToken()}
	input["operation_id"] = receipt.Operation
	initial, _ := json.Marshal(receipt)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec("INSERT OR IGNORE INTO deploy_meta(key,value) VALUES(?,?)", key, string(initial))
	if err != nil {
		return "", err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if count != 1 {
		return "", fmt.Errorf("此变更已派发，请核对原执行单，禁止重复调度写入")
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO deploy_meta(key,value) VALUES(?,?)", xxlChangeTouchKey(plan.ChangeID), "1"); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if err := xxlCall(ctx, binding.Connection, "apply", input, &receipt.Task); err != nil {
		if errors.Is(err, errXXLJobRejected) {
			if clearErr := s.clearRejectedXXLJobEffect(plan, key, string(initial)); clearErr == nil {
				return "", err
			}
		}
		return "", fmt.Errorf("%v；%w", err, errXXLJobUncertain)
	}
	task := receipt.Task
	if !validXXLJobReceipt(plan, task) || (input["task_id"] != nil && input["task_id"] != task.ID) {
		return "", errXXLJobUncertain
	}
	stored, _ := json.Marshal(receipt)
	res, err = s.db.Exec("UPDATE deploy_meta SET value=? WHERE key=? AND value=?", string(stored), key, string(initial))
	if err != nil {
		return "", errXXLJobUncertain
	}
	if count, err = res.RowsAffected(); err != nil || count != 1 {
		return "", errXXLJobUncertain
	}
	return xxlEffectDetail(plan, task), nil
}

func (s *Store) clearRejectedXXLJobEffect(plan *XXLJobPlan, key, expected string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("DELETE FROM deploy_meta WHERE key=? AND value=?", key, expected)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrRunConflict
	}
	var active int
	if err := tx.QueryRow("SELECT COUNT(*) FROM deploy_meta WHERE key LIKE ?", xxlEffectKey(plan.ChangeID, "%")).Scan(&active); err != nil {
		return err
	}
	if active == 0 {
		if _, err := tx.Exec("DELETE FROM deploy_meta WHERE key=?", xxlChangeTouchKey(plan.ChangeID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validXXLJobReceipt(plan *XXLJobPlan, task XXLJobTask) bool {
	return task.ID > 0 && task.GroupID == plan.GroupID && task.Revision != "" &&
		(plan.Before == nil || plan.Before.ID == task.ID) &&
		(plan.Action == "enable" && task.TriggerStatus == 1 ||
			(plan.Action == "create" || plan.Action == "disable") && task.TriggerStatus == 0 ||
			plan.Action == "update")
}

func (s *Store) reconcileXXLJobStep(run *DeliveryRun, nodeID string) (bool, error) {
	for _, plan := range run.Plan.XXLJobPlans {
		if nodeID != "" && plan.NodeID != nodeID {
			continue
		}
		for i := range run.Steps {
			step := &run.Steps[i]
			if step.NodeID != plan.NodeID || step.Status != "uncertain" && step.Status != "executing" {
				continue
			}
			if step.LeaseUntil > time.Now().Unix() {
				continue
			}
			effect, err := s.xxlEffect(plan.ChangeID, plan.Environment)
			if err != nil {
				return false, err
			}
			if effect == nil || effect.RunID != run.ID || effect.Digest != digestJSON(plan) {
				return false, errXXLJobUncertain
			}
			if !validXXLJobReceipt(plan, effect.Task) {
				if err := s.recoverXXLJobReceipt(plan, effect); err != nil {
					return false, err
				}
			}
			step.Status, step.Detail, step.EndedAt, step.LeaseUntil = "success", xxlEffectDetail(plan, effect.Task), time.Now().Unix(), 0
			if !slices.ContainsFunc(run.Steps, func(item DeliveryStep) bool {
				return item.Status != "success" && !(run.Plan.ExecutionMode == dependencyExecution && scheduleStepDone(item))
			}) {
				run.Status = deliveryCompletedStatus(run)
			}
			return true, s.saveDeliveryRun(run)
		}
	}
	return false, nil
}

func (s *Store) recoverXXLJobReceipt(plan *XXLJobPlan, effect *xxlJobEffect) error {
	if effect.Operation == "" {
		return errXXLJobUncertain
	}
	binding, err := s.xxlJobBinding(plan.Service, plan.Environment, plan.Alias)
	if err != nil {
		return errXXLJobUncertain
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	group, err := binding.group(ctx)
	if err != nil || s.xxlBindingDigest(binding, group) != plan.BindingDigest {
		return errXXLJobUncertain
	}
	before, _ := json.Marshal(effect)
	input := xxlGroupInput(group)
	input["operation_id"] = effect.Operation
	if err := xxlCall(ctx, binding.Connection, "receipt", input, &effect.Task); err != nil || !validXXLJobReceipt(plan, effect.Task) {
		return errXXLJobUncertain
	}
	if plan.CreatedBy > 0 {
		creation, err := s.xxlEffect(plan.CreatedBy, plan.Environment)
		if err != nil || creation == nil || creation.RunID != effect.RunID || creation.Task.ID != effect.Task.ID {
			return errXXLJobUncertain
		}
	}
	after, _ := json.Marshal(effect)
	res, err := s.db.Exec("UPDATE deploy_meta SET value=? WHERE key=? AND value=?", string(after), xxlEffectKey(plan.ChangeID, plan.Environment), string(before))
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil || count != 1 {
		return ErrRunConflict
	}
	return nil
}

func xxlEffectDetail(plan *XXLJobPlan, task XXLJobTask) string {
	return fmt.Sprintf("%s / %s / 任务 #%d · %s", plan.Connection, plan.AppName, task.ID,
		map[string]string{"create": "已创建，调度停用", "update": "配置已更新", "enable": "调度已启用", "disable": "调度已停用"}[plan.Action])
}
