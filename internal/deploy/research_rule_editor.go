package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	policy "logplatform/pkg/releasepolicy"
	"net/http"
	"strconv"
	"time"
)

func researchPolicyValues(w *WorkItem) map[string]string {
	return map[string]string{"kind": w.Kind, "product": w.SourceProductID, "project": w.ProjectID, "status": w.ExternalStatus, "resolution": w.Resolution, "severity": w.Severity, "priority": w.Priority, "title": w.Title}
}
func portableResearchRules(v ResearchReleaseRules) policy.Policy {
	if v.Policy != nil {
		return *v.Policy
	}
	p := policy.Policy{Version: 1, Default: v.Default, RequiredFields: nil, Rules: []policy.Rule{}}
	if v.Enabled || v.Revision > 0 || len(v.Projects) > 0 {
		p.RequiredFields = []string{"project"}
	}
	for _, r := range v.Projects {
		name := r.Name
		if name == "" {
			name = "项目 #" + r.ProjectID
		}
		p.Rules = append(p.Rules, policy.Rule{Name: name, Match: "all", Conditions: []policy.Condition{{Field: "project", Operator: "in", Values: []string{r.ProjectID}}}, Decision: r.Decision})
	}
	return p
}
func registerResearchRuleEditor(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/research/connections/{name}/release-rules/options", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可配置识别规则")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		z, c, _, err := s.liveReadClient(ctx, r.PathValue("name"), deliveryActor(r), false)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		options, err := z.researchRuleOptions(ctx, c)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, map[string]any{"fields": policy.Fields, "options": options})
	})
	mux.HandleFunc("POST /api/research/connections/{name}/release-rules/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可预览识别规则")
			return
		}
		var req struct {
			ResearchReleaseRules
			BugID string `json:"bug_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil || req.Policy == nil {
			deliveryWriteError(w, 400, "请填写有效规则和 Bug 编号")
			return
		}
		if err := policy.Validate(*req.Policy); err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		id, err := strconv.ParseInt(req.BugID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != req.BugID {
			deliveryWriteError(w, 400, "请输入有效的 Bug 编号")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		z, c, _, err := s.liveReadClient(ctx, r.PathValue("name"), deliveryActor(r), false)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		remote, err := z.researchRead(ctx, req.BugID, "view")
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		product, _ := strconv.Atoi(researchValue(remote.Bug, "product"))
		if !liveProductAllowed(c, product) {
			deliveryWriteError(w, 403, "Bug 不在允许的产品范围")
			return
		}
		fields := map[string]string{"kind": "bug"}
		for dest, src := range map[string]string{"project": "project", "product": "product", "status": "status", "resolution": "resolution", "severity": "severity", "priority": "pri", "title": "title"} {
			fields[dest] = researchValue(remote.Bug, src)
		}
		result := policy.Evaluate(*req.Policy, fields)
		if !req.Enabled {
			result = policy.Result{Decision: "unknown", Reason: "自动识别未启用"}
		}
		effective := result
		local, err := s.liveLocalReference(c.Name, req.BugID)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		if local != nil && local.ReleaseRequired != nil {
			effective = policy.Result{Decision: "not_independent", Reason: "人工调整"}
			if local.NoReleaseReason != "" {
				effective.Reason += "：" + local.NoReleaseReason
			}
			if *local.ReleaseRequired {
				effective.Decision = "independent"
			}
		}
		// Preview only returns fields used by the submitted rule, never description or attachments.
		used := map[string]string{}
		for _, rule := range req.Policy.Rules {
			for _, cond := range rule.Conditions {
				used[cond.Field] = fields[cond.Field]
			}
		}
		deliveryJSON(w, map[string]any{"result": result, "effective_result": effective, "bug_id": req.BugID, "title": researchValue(remote.Bug, "title"), "fields": used, "note": fmt.Sprintf("基于版本 %d 的当前配置预览；本次不保存或执行发布", req.Revision)})
	})
}
