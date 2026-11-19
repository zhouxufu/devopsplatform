package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/workflownode"
	"strconv"
	"time"
)

type workflowAPIReport struct {
	Service string `json:"service"`
	RunID   int64  `json:"run_id"`
	Owner   string `json:"owner"`
}

func (s *Store) prepareAPITestWorkflow(b *workflownode.Binding, node DeliveryNode, apps []DeliveryService) (*workflownode.Binding, error) {
	if b.Provider != workflownode.APITestProvider {
		return nil, fmt.Errorf("接口测试执行器仅接受宿主登记的共享插件")
	}
	for _, app := range apps {
		if node.Service != "" && node.Service != app.Service {
			continue
		}
		sv := s.GetService(app.Service)
		if sv == nil {
			return nil, fmt.Errorf("应用不存在")
		}
		p, e := s.apiTestingProfile(sv.ApplicationID, b.Environment)
		if e != nil {
			return nil, e
		}
		env := s.GetServiceEnvironment(sv.Name, b.Environment)
		if env == nil || !p.Enabled || p.RegressionSuiteID == 0 || p.RegressionTargetKey == "" {
			return nil, fmt.Errorf("%s：请在接口测试的连接与认证中配置交付回归测试集和目标", sv.Name)
		}
		suite, e := s.apiTestSuite(sv.ApplicationID, "", p.RegressionSuiteID, false)
		if e != nil || suite.Visibility != "team" {
			return nil, fmt.Errorf("%s：交付回归必须使用团队测试集", sv.Name)
		}
		task := workflownode.TestTask{Service: sv.Name, ApplicationID: sv.ApplicationID, Environment: b.Environment, ArtifactID: app.Artifact.ID, ArtifactDigest: app.Artifact.Digest, SuiteID: suite.ID, SuiteRevision: suite.Revision, ProfileRevision: p.Revision, TargetKey: p.RegressionTargetKey, CaseRevisions: map[string]int64{}}
		units, steps := 0, 0
		var bindingCases []*apiTestCase
		for _, id := range suite.CaseIDs {
			c, e := s.apiTestCase(sv.ApplicationID, "", id)
			if e != nil || c.Visibility != "team" {
				return nil, fmt.Errorf("测试集包含不可共享的用例，请重新配置")
			}
			if e = validateAPITestCase(c); e != nil {
				return nil, e
			}
			if e = s.validateCaseDocumentLinks(sv.ApplicationID, c); e != nil {
				return nil, e
			}
			bindingCases = append(bindingCases, c)
			task.CaseRevisions[strconv.FormatInt(id, 10)] = c.Revision
			n := len(c.DataRows)
			if n == 0 {
				n = 1
			}
			units += n
			steps += n * len(c.Steps)
		}
		if e := validateAPITestBindings(suite.Bindings, bindingCases); e != nil {
			return nil, e
		}
		if units == 0 || units > 20 || steps > 100 {
			return nil, fmt.Errorf("测试集应包含 1–20 个数据组合且不超过 100 步")
		}
		for _, target := range s.apiTestTargets(sv, env, p) {
			if target.Key == task.TargetKey {
				task.TargetConfiguration = target.Configuration
			}
		}
		if task.TargetConfiguration == "" {
			return nil, fmt.Errorf("%s：交付回归目标已失效，请重新配置", sv.Name)
		}
		variables, e := s.apiTestVariables(sv, env)
		if e != nil {
			return nil, e
		}
		task.VariablesDigest = s.privateOperationDigest(variables)
		b.Tests = append(b.Tests, task)
	}
	if len(b.Tests) == 0 || len(b.Tests) > 10 {
		return nil, fmt.Errorf("测试节点应匹配 1–10 个应用")
	}
	return b, nil
}
func (s *Store) validateAPITestWorkflowTask(t workflownode.TestTask, requireArtifact bool) (*Service, apiTestInput, error) {
	sv := s.GetService(t.Service)
	var in apiTestInput
	if sv == nil || sv.ApplicationID != t.ApplicationID {
		return nil, in, fmt.Errorf("应用身份已变化")
	}
	p, e := s.apiTestingProfile(t.ApplicationID, t.Environment)
	if e != nil {
		return nil, in, e
	}
	env := s.GetServiceEnvironment(t.Service, t.Environment)
	if env == nil || !p.Enabled || p.Revision != t.ProfileRevision || p.RegressionSuiteID != t.SuiteID || p.RegressionTargetKey != t.TargetKey {
		return nil, in, fmt.Errorf("测试连接、认证或交付回归配置已变化，请重新预览")
	}
	suite, e := s.apiTestSuite(t.ApplicationID, "", t.SuiteID, false)
	if e != nil || suite.Visibility != "team" || suite.Revision != t.SuiteRevision {
		return nil, in, fmt.Errorf("测试集已变化，请重新预览")
	}
	for _, id := range suite.CaseIDs {
		c, e := s.apiTestCase(t.ApplicationID, "", id)
		if e != nil || c.Visibility != "team" || t.CaseRevisions[strconv.FormatInt(id, 10)] != c.Revision {
			return nil, in, fmt.Errorf("测试用例已变化，请重新预览")
		}
	}
	variables, e := s.apiTestVariables(sv, env)
	if e != nil {
		return nil, in, e
	}
	if s.privateOperationDigest(variables) != t.VariablesDigest {
		return nil, in, fmt.Errorf("绑定变量或认证凭据已变化，请重新预览")
	}
	for _, target := range s.apiTestTargets(sv, env, p) {
		if target.Key != t.TargetKey {
			continue
		}
		if target.Configuration != t.TargetConfiguration {
			return nil, in, fmt.Errorf("测试目标配置已变化，请重新预览")
		}
		if requireArtifact && (t.ArtifactID == 0 || t.ArtifactDigest == "" || target.ArtifactID != t.ArtifactID || target.ArtifactDigest != t.ArtifactDigest) {
			return nil, in, fmt.Errorf("目标尚未记录当前审核制品的成功部署；请将测试节点放在部署之后并核对目标")
		}
		in = apiTestInput{Environment: t.Environment, TargetKey: t.TargetKey, TargetFingerprint: target.Fingerprint, SuiteID: t.SuiteID, SuiteRevision: t.SuiteRevision, CaseRevisions: t.CaseRevisions, Reason: "已审核编排的交付回归"}
		return sv, in, nil
	}
	return nil, in, fmt.Errorf("已审核测试目标不存在")
}
func (s *Store) advanceAPITestWorkflow(run *DeliveryRun, step *DeliveryStep, node DeliveryNode, actor string) error {
	b := node.Extension
	if step.Status == "executing" {
		s.reconcileAPITestWorkflow(run, node)
		if latest := s.DeliveryRun(run.ID); latest != nil {
			for _, current := range latest.Steps {
				if current.NodeID == node.ID && current.Status != "executing" {
					*run = *latest
					return nil
				}
			}
		}
		return fmt.Errorf("测试节点已启动，结果可在报告中核对；不会重复发送请求")
	}
	d, e := describeWorkflowProvider(context.Background(), b.Provider)
	if e != nil || d == nil || b.Provider != workflownode.APITestProvider || d.ProviderDigest != b.ProviderDigest || b.Executor != d.Executor || len(b.Tests) == 0 {
		return fmt.Errorf("接口测试插件不可用或已变化，请重新预览")
	}
	if _, e = s.validateDeliveryExecution(run); e != nil {
		return e
	}
	for _, task := range b.Tests {
		if task.Environment != run.Plan.Environment.Name {
			return fmt.Errorf("测试环境与审核计划不一致")
		}
		if _, _, e = s.validateAPITestWorkflowTask(task, true); e != nil {
			return e
		}
	}
	if s.apiTests == nil {
		return fmt.Errorf("接口测试执行器不可用")
	}
	s.apiTests.mu.Lock()
	defer s.apiTests.mu.Unlock()
	if s.apiTests.closing {
		return fmt.Errorf("服务正在停止")
	}
	step.Status = "executing"
	step.StartedAt = time.Now().Unix()
	step.LeaseUntil = step.StartedAt + 2*60*60
	step.Detail = "正在执行已审核的测试集与用例修订，不自动重试"
	run.Status = "running"
	if e = s.saveDeliveryRun(run); e != nil {
		return e
	}
	// Copy frozen data before a background worker; never mutate a caller's run.
	raw, _ := json.Marshal(b)
	var frozen workflownode.Binding
	_ = json.Unmarshal(raw, &frozen)
	s.Audit(actor, "start-api-test-node", "delivery-run", fmt.Sprint(run.ID), map[string]any{"node": node.ID, "provider": b.Provider})
	s.apiTests.wg.Add(1)
	go func() { defer s.apiTests.wg.Done(); s.runAPITestWorkflow(run.ID, node.ID, actor, &frozen) }()
	return nil
}
func (s *Store) attachAPIWorkflowReport(runID int64, nodeID string, report workflowAPIReport) error {
	for n := 0; n < 4; n++ {
		r := s.DeliveryRun(runID)
		if r == nil || r.Status != "running" {
			return fmt.Errorf("编排已停止")
		}
		for i := range r.Steps {
			step := &r.Steps[i]
			if step.NodeID != nodeID {
				continue
			}
			if step.Status != "executing" {
				return fmt.Errorf("测试节点已停止")
			}
			for _, old := range step.APIReports {
				if old.RunID == report.RunID {
					return nil
				}
			}
			step.APIReports = append(step.APIReports, report)
		}
		if e := s.saveDeliveryRun(r); e == nil {
			return nil
		}
	}
	return fmt.Errorf("测试报告关联冲突，需人工核对")
}
func (s *Store) runAPITestWorkflow(runID int64, nodeID, owner string, b *workflownode.Binding) {
	for index, task := range b.Tests {
		run := s.DeliveryRun(runID)
		if run == nil || run.Status != "running" {
			return
		}
		if _, e := s.validateDeliveryExecution(run); e != nil {
			s.finishWorkflowExtension(runID, nodeID, false, false, e.Error())
			return
		}
		sv, in, e := s.validateAPITestWorkflowTask(task, true)
		if e != nil {
			s.finishWorkflowExtension(runID, nodeID, false, false, e.Error())
			return
		}
		in.expectedVariablesDigest = task.VariablesDigest
		in.deliveryRunID = runID
		in.deliveryNodeID = nodeID
		in.RequestID = workflowExecutionID(runID, nodeID+fmt.Sprint(":", index))
		in.Reason += " #" + fmt.Sprint(runID)
		result, e := s.apiTests.startAuthorized(sv, owner, false, in, true)
		if e != nil {
			s.finishWorkflowExtension(runID, nodeID, false, false, e.Error())
			return
		}
		if e = s.attachAPIWorkflowReport(runID, nodeID, workflowAPIReport{Service: sv.Name, RunID: result.ID, Owner: owner}); e != nil {
			s.apiTests.stop(result.ID)
			s.finishWorkflowExtension(runID, nodeID, false, true, e.Error())
			return
		}
		for {
			time.Sleep(100 * time.Millisecond)
			s.apiTests.mu.Lock()
			closing := s.apiTests.closing
			_, jobActive := s.apiTests.jobs[result.ID]
			s.apiTests.mu.Unlock()
			if closing {
				s.apiTests.stop(result.ID)
				s.finishWorkflowExtension(runID, nodeID, false, true, "服务停止，测试结果待核对，不自动重发")
				return
			}
			current := s.DeliveryRun(runID)
			if current == nil || current.Status != "running" {
				s.apiTests.stop(result.ID)
				return
			}
			v, e := s.apiTests.load(sv.ApplicationID, owner, result.ID)
			if e != nil {
				s.apiTests.stop(result.ID)
				s.finishWorkflowExtension(runID, nodeID, false, true, "测试结果读取失败，请核对原报告")
				return
			}
			if v.Status == "queued" || v.Status == "running" {
				if !jobActive {
					s.finishWorkflowExtension(runID, nodeID, false, true, "执行已中断，结果待核对，不自动重发")
					return
				}
				continue
			}
			if v.Status != "success" {
				s.finishWorkflowExtension(runID, nodeID, false, v.Status == "unknown", "测试未通过或结果待核对（"+v.Status+"），已停止后续应用；请查看执行报告")
				return
			}
			// A report may be durable just before the manager releases its concurrency slot.
			s.apiTests.mu.Lock()
			_, active := s.apiTests.jobs[result.ID]
			s.apiTests.mu.Unlock()
			if active {
				continue
			}
			break
		}
	}
	s.finishWorkflowExtension(runID, nodeID, true, false, "全部接口测试通过，可查看用例执行报告；此结果不代表当前运行健康")
}
func (m *apiTestManager) stop(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel := m.jobs[id]; cancel != nil {
		cancel()
	}
}

// Read-only recovery. A missing report or a partial batch never starts another request.
func (s *Store) reconcileAPITestWorkflow(run *DeliveryRun, node DeliveryNode) {
	var step *DeliveryStep
	for i := range run.Steps {
		if run.Steps[i].NodeID == node.ID {
			step = &run.Steps[i]
		}
	}
	if step == nil || len(step.APIReports) != len(node.Extension.Tests) {
		return
	}
	for _, ref := range step.APIReports {
		sv := s.GetService(ref.Service)
		if sv == nil {
			return
		}
		v, e := s.apiTests.load(sv.ApplicationID, ref.Owner, ref.RunID)
		if e != nil {
			return
		}
		if v.Status == "queued" || v.Status == "running" {
			return
		}
		if v.Status != "success" {
			s.finishWorkflowExtension(run.ID, node.ID, false, v.Status == "unknown", "测试未通过或结果待核对，请查看原报告")
			return
		}
	}
	s.finishWorkflowExtension(run.ID, node.ID, true, false, "全部接口测试已通过，依据原执行报告核对完成")
}
