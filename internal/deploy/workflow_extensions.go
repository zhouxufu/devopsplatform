package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/extensionclient"
	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func describeWorkflowProvider(ctx context.Context, id string) (*workflownode.Descriptor, error) {
	if d := workflownode.SharedDescriptor(id, os.Getenv(workflownode.SharedWorkspacesEnv)); d != nil {
		return d, nil
	}
	client, err := extensionclient.FromEnvironment()
	if err != nil {
		return nil, err
	}
	out, err := client.Invoke(ctx, id, p.Request{Capability: workflownode.Capability, Operation: "describe"})
	if err != nil {
		return nil, err
	}
	if out.Provider == nil {
		return nil, fmt.Errorf("平台未返回节点插件安装身份")
	}
	var d workflownode.Descriptor
	if json.Unmarshal(out.Data, &d) != nil {
		return nil, fmt.Errorf("插件节点描述不兼容")
	}
	if out.Provider != nil {
		d.ProviderVersion = out.Provider.Version
		d.ProviderDigest = out.Provider.Digest
	}
	if err := workflownode.ValidateDescriptor(d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Only host-approved domain executors are allowed; no plugin-returned script is executed.
func (s *Store) prepareWorkflowExtension(node DeliveryNode, apps []DeliveryService, environments ...string) (*workflownode.Binding, error) {
	if node.Extension == nil || node.Extension.Provider == "" || node.Extension.NodeKey == "" {
		return nil, fmt.Errorf("请选择已安装并启用的节点插件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d, err := describeWorkflowProvider(ctx, node.Extension.Provider)
	if err != nil {
		return nil, err
	}
	if d.Key != node.Extension.NodeKey {
		return nil, fmt.Errorf("此插件节点尚无兼容执行器")
	}
	b := &workflownode.Binding{Provider: node.Extension.Provider, NodeKey: d.Key, Executor: d.Executor}
	b.Descriptor = d
	b.ProviderVersion = d.ProviderVersion
	b.ProviderDigest = d.ProviderDigest
	if len(environments) > 0 {
		b.Environment = environments[0]
	}
	if d.Executor == workflownode.APITestExecutor {
		return s.prepareAPITestWorkflow(b, node, apps)
	}
	if d.Executor == workflownode.RemoteExecutor {
		b.Parameters = map[string]string{}
		allowed := map[string]bool{}
		for _, f := range d.Fields {
			value := node.Extension.Parameters[f.Key]
			if len(value) > 2048 || f.Required && strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("插件参数 %s 未填写或过长", f.Label)
			}
			allowed[f.Key] = true
			b.Parameters[f.Key] = value
		}
		for key := range node.Extension.Parameters {
			if !allowed[key] {
				return nil, fmt.Errorf("插件参数与当前声明不一致")
			}
		}
		for _, app := range apps {
			if node.Service != "" && node.Service != app.Service {
				continue
			}
			sv := s.GetService(app.Service)
			if sv == nil {
				return nil, fmt.Errorf("应用不存在")
			}
			b.Targets = append(b.Targets, workflownode.Target{Service: app.Service, ApplicationID: sv.ApplicationID, ArtifactID: app.Artifact.ID, ArtifactDigest: app.Artifact.Digest})
		}
		if len(b.Targets) == 0 {
			return nil, fmt.Errorf("插件节点没有匹配的交付应用")
		}
		return b, nil
	}

	for _, app := range apps {
		if node.Service != "" && node.Service != app.Service {
			continue
		}
		a := s.GetArtifact(app.Artifact.ID)
		if a == nil || a.Source != "pipeline-run" || a.Digest != app.Artifact.Digest {
			return nil, fmt.Errorf("%s：文档生成需要带原构建快照的制品", app.Service)
		}
		sv, _, commit, err := s.buildReplay(a.SourceID)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", app.Service, err)
		}
		source, err := s.resolveDocumentSource(sv, b.Environment)
		if err != nil {
			return nil, err
		}
		if source.Provider != b.Provider {
			return nil, fmt.Errorf("%s：请在接口文档中一次性绑定此文档来源", app.Service)
		}
		plan, err := prepareDocumentPlan(ctx, sv, source)
		if err != nil {
			return nil, err
		}
		if b.ProviderVersion != "" && b.ProviderVersion != plan.ProviderVersion {
			return nil, fmt.Errorf("插件版本在预览期间变化，请重试")
		}
		b.ProviderVersion = plan.ProviderVersion
		b.ProviderDigest = plan.ProviderDigest
		b.Documents = append(b.Documents, workflownode.DocumentTask{Environment: b.Environment, Service: sv.Name, ApplicationID: sv.ApplicationID, ArtifactID: a.ID, ArtifactDigest: a.Digest, SourceRunID: a.SourceID, Commit: commit, Source: source, Plan: *plan})
	}
	if len(b.Documents) == 0 {
		return nil, fmt.Errorf("插件节点没有匹配的交付应用")
	}
	return b, nil
}
func (s *Store) advanceWorkflowExtension(run *DeliveryRun, step *DeliveryStep, node DeliveryNode, actor string) error {
	if node.Extension != nil && node.Extension.Executor == workflownode.APITestExecutor {
		return s.advanceAPITestWorkflow(run, step, node, actor)
	}
	if step.Status == "executing" {
		if node.Extension != nil && node.Extension.Executor == workflownode.RemoteExecutor {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			status, err := invokeWorkflowJob(ctx, node.Extension, workflowExecutionID(run.ID, node.ID), "job.status")
			if err != nil {
				return fmt.Errorf("插件任务结果仍待核对：%w", err)
			}
			if status.State == "success" || status.State == "failed" || status.State == "canceled" {
				s.finishWorkflowExtension(run.ID, node.ID, status.State == "success", false, status.Message)
				if latest := s.DeliveryRun(run.ID); latest != nil {
					*run = *latest
				}
				return nil
			}
			return fmt.Errorf("插件任务尚未完成：%s", status.State)
		}
		return fmt.Errorf("插件节点正在执行或中断待核对，不会重复启动")
	}
	b := node.Extension
	if b == nil || b.Executor != apidoc.Capability && b.Executor != workflownode.RemoteExecutor || b.Executor == apidoc.Capability && len(b.Documents) == 0 {
		return fmt.Errorf("插件节点缺少已冻结执行计划")
	}
	pr := s.pipelineRunner
	if pr == nil {
		return fmt.Errorf("当前服务未配置插件节点执行器")
	}
	if b.Executor == workflownode.RemoteExecutor {
		d, err := describeWorkflowProvider(context.Background(), b.Provider)
		if err != nil {
			return err
		}
		if b.Descriptor == nil || d.ProviderVersion != b.ProviderVersion || d.ProviderDigest != b.ProviderDigest {
			return fmt.Errorf("插件安装包已变化，请重新预览并审核")
		}
	}
	// Check provider availability/version before claiming the durable execution marker.
	for _, task := range b.Documents {
		sv := s.GetService(task.Service)
		if sv == nil || sv.ApplicationID != task.ApplicationID {
			return fmt.Errorf("应用身份已变化")
		}
		current, err := prepareDocumentPlan(context.Background(), sv, task.Source)
		if err != nil {
			return err
		}
		a, _ := json.Marshal(current)
		frozen, _ := json.Marshal(task.Plan)
		if string(a) != string(frozen) {
			return fmt.Errorf("插件或生成计划已变化，请重新预览并审核")
		}
	}
	step.Status = "executing"
	step.Detail = "插件任务已启动，使用已审核应用、环境与制品范围"
	step.StartedAt = time.Now().Unix()
	step.LeaseUntil = step.StartedAt + 40*60
	run.Status = "running"
	if err := s.saveDeliveryRun(run); err != nil {
		return err
	}
	s.Audit(actor, "start-extension-node", "delivery-run", fmt.Sprint(run.ID), map[string]any{"provider": b.Provider, "provider_version": b.ProviderVersion, "node": node.ID})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		defer cancel()
		var executionErr error
		if b.Executor == workflownode.RemoteExecutor {
			unknown, err := runWorkflowJob(ctx, b, workflowExecutionID(run.ID, node.ID))
			detail := "插件任务已完成"
			if err != nil {
				detail = err.Error()
			}
			s.finishWorkflowExtension(run.ID, node.ID, err == nil, unknown, detail)
			return
		}
		for _, task := range b.Documents {
			if executionErr = pr.generateWorkflowDocument(ctx, run.ID, node.ID, task); executionErr != nil {
				break
			}
		}
		detail := "接口文档快照已关联所选制品，可在接口文档页查看"
		if executionErr != nil {
			detail = "插件任务失败：" + executionErr.Error()
		}
		s.finishWorkflowExtension(run.ID, node.ID, executionErr == nil, false, detail)
	}()
	return nil
}
func (pr *PipelineRunner) generateWorkflowDocument(ctx context.Context, deliveryID int64, nodeID string, task workflownode.DocumentTask) error {
	a := pr.st.GetArtifact(task.ArtifactID)
	if a == nil || a.Digest != task.ArtifactDigest || a.Service != task.Service || a.SourceID != task.SourceRunID {
		return fmt.Errorf("已审核制品身份已变化")
	}
	sv, delivery, commit, err := pr.st.buildReplay(task.SourceRunID)
	if err != nil {
		return err
	}
	if sv.ApplicationID != task.ApplicationID || commit != task.Commit {
		return fmt.Errorf("原构建源码身份不匹配")
	}
	sv, err = pr.st.prepareServiceResources(sv, delivery)
	if err != nil {
		return err
	}
	runner := pr.st.GetRunner(delivery.Runner)
	if runner == nil || !runner.Enabled {
		return fmt.Errorf("构建 Runner 不可用")
	}
	release, ok := pr.claimRunner(runner.Name, runner.MaxParallel)
	if !ok {
		return fmt.Errorf("Runner 正忙，请核对后重新提交插件任务")
	}
	defer release()
	root, err := os.MkdirTemp(pr.b.workspaceDir, "document-job-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	builder := *pr.b
	builder.workspaceDir = root
	workspace := builder.buildDir(sv)
	outputs := filepath.Join(root, "outputs")
	if err := os.MkdirAll(outputs, 0700); err != nil {
		return err
	}
	checkout := pr.lib.Get("git-checkout")
	if checkout == nil {
		return fmt.Errorf("缺少标准源码检出步骤")
	}
	env := checkout.BuildEnv(StepInst{Params: map[string]string{"GIT_URL": sv.GitURL, "BRANCH": commit}}, workspace, sv.Name, commit, outputs)
	for k, v := range sv.RuntimeEnv {
		env = append(env, k+"="+v)
	}
	var diagnostic strings.Builder
	emit := func(line string) {
		for _, secret := range sv.RuntimeSecrets {
			if secret != "" {
				line = strings.ReplaceAll(line, secret, "[REDACTED]")
			}
		}
		if diagnostic.Len() < 16000 {
			diagnostic.WriteString(line)
		}
	}
	if code := builder.execBuildStepContext(ctx, sv, checkout, env, emit); code != 0 {
		return fmt.Errorf("文档源码检出失败：%s", diagnostic.String())
	}
	sv.APIDocumentation = &apidoc.Frozen{Environment: task.Environment, Source: task.Source, Plan: &task.Plan}
	sv.APIDocumentation.Source.Required = true
	job := &PipelineRunner{st: pr.st, b: &builder, hub: pr.hub, lib: pr.lib}
	if err := job.generateAPIDocumentWithEmit(ctx, task.SourceRunID, sv, a, workspace, outputs, emit); err != nil {
		return fmt.Errorf("文档生成失败：%s", diagnostic.String())
	}
	return nil
}
