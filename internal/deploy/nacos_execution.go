package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type NacosConfigPlan struct {
	ChangeID          int64       `json:"change_id"`
	Service           string      `json:"service"`
	Alias             string      `json:"alias"`
	Connection        string      `json:"connection"`
	Endpoint          string      `json:"endpoint"`
	DestinationDigest string      `json:"destination_digest"`
	ChangeDigest      string      `json:"change_digest"`
	DataID            string      `json:"data_id"`
	Group             string      `json:"group"`
	Namespace         string      `json:"namespace"`
	Format            string      `json:"format"`
	BeforeMD5         string      `json:"before_md5"`
	AfterMD5          string      `json:"after_md5"`
	BeforeEncrypted   string      `json:"before_encrypted,omitempty"`
	AfterEncrypted    string      `json:"after_encrypted,omitempty"`
	Restore           bool        `json:"restore,omitempty"`
	Diff              []NacosDiff `json:"diff"`
}
type NacosExecutionPlan struct {
	NodeID  string            `json:"node_id"`
	Configs []NacosConfigPlan `json:"configs"`
}

// Only persistence includes encrypted full documents. Public previews expose
// selected-key differences, never backups or credentials.
func publicNacosPlans(plans []*NacosExecutionPlan) []*NacosExecutionPlan {
	if plans == nil {
		return nil
	}
	out := make([]*NacosExecutionPlan, 0, len(plans))
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		p := *plan
		p.Configs = append([]NacosConfigPlan{}, plan.Configs...)
		for i := range p.Configs {
			p.Configs[i].BeforeEncrypted = ""
			p.Configs[i].AfterEncrypted = ""
		}
		out = append(out, &p)
	}
	return out
}

func nacosTargetKey(p NacosConfigPlan) string {
	return digestJSON([]any{p.Endpoint, p.Namespace, p.Group, p.DataID})
}

func (s *Store) nacosBinding(service, environment, alias string) (*ResourceConnection, error) {
	if !s.hasResourceApplication(service) || s.GetEnvironment(environment) == nil {
		return nil, fmt.Errorf("应用或环境不存在")
	}
	resources := s.ApplicationResources(service)
	found := false
	for _, r := range resources.Requirements {
		if r.Alias == alias && r.Kind == "nacos" {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("应用未声明 Nacos 资源 %s", alias)
	}
	for _, b := range resources.Bindings {
		if b.Alias == alias && b.Environment == environment {
			c := s.GetConnection(b.Connection)
			if c == nil || c.Kind != "nacos" {
				return nil, fmt.Errorf("Nacos 环境连接不存在")
			}
			if len(b.Config) > 0 {
				return nil, fmt.Errorf("命名空间在 Nacos 连接中指定，暂不支持绑定覆盖")
			}
			return c, nil
		}
	}
	return nil, fmt.Errorf("应用 %s 的资源 %s 未绑定 %s 环境的 Nacos 连接", service, alias, environment)
}

func (s *Store) prepareNacosNode(ctx context.Context, nodeID string, ids []int64, environment string) (*NacosExecutionPlan, error) {
	if len(ids) == 0 || len(ids) > 20 {
		return nil, fmt.Errorf("Nacos 节点选择 1–20 项变更")
	}
	p := &NacosExecutionPlan{NodeID: nodeID, Configs: []NacosConfigPlan{}}
	seen := map[string]bool{}
	for _, id := range ids {
		change := s.GetChange(id)
		if change == nil || change.Kind != "nacos" || change.Nacos == nil {
			return nil, fmt.Errorf("Nacos 变更 #%d 没有结构化配置项", id)
		}
		if err := normalizeNacosChange(change); err != nil {
			return nil, err
		}
		connection, err := s.nacosBinding(change.Service, environment, change.Nacos.ResourceAlias)
		if err != nil {
			return nil, err
		}
		n, err := newNacosClient(ctx, connection)
		if err != nil {
			return nil, err
		}
		before, err := n.read(ctx, change.Nacos.DataID, change.Nacos.Group)
		if err != nil {
			return nil, err
		}
		after, diff, err := patchNacosContent(before, change.Nacos)
		if err != nil {
			return nil, err
		}
		if change.Nacos.Restore != nil {
			previous, previousEnv, err := s.nacosRestoreSource(change.Nacos.Restore)
			if err != nil {
				return nil, err
			}
			if previousEnv != environment || previous.Service != change.Service || previous.Alias != change.Nacos.ResourceAlias || previous.DataID != change.Nacos.DataID || previous.Group != change.Nacos.Group || previous.Format != change.Nacos.Format || previous.DestinationDigest != sqlDestination(connection) || previous.AfterMD5 != nacosMD5(before) {
				return nil, fmt.Errorf("恢复目标或新版本配置已变化，禁止恢复；请重新核对")
			}
			expected := nacosInverseEntries(previous.Diff)
			if digestJSON(expected) != digestJSON(change.Nacos.Entries) {
				return nil, fmt.Errorf("恢复配置项不允许修改，请另建普通变更")
			}
			after, err = s.decryptSecret(previous.BeforeEncrypted)
			if err != nil || nacosMD5(after) != previous.BeforeMD5 {
				return nil, fmt.Errorf("恢复备份无法解密或摘要不符")
			}
			// Restore uses the exact backup, not the inverse patch serialization.
			// Reverse the original typed diff as well (e.g. YAML False / 0x10),
			// so the preview cannot show a string where the backup has a scalar.
			diff = []NacosDiff{}
			for _, original := range previous.Diff {
				diff = append(diff, NacosDiff{Key: original.Key, Before: original.After, After: original.Before})
			}
		}
		item := NacosConfigPlan{ChangeID: id, Service: change.Service, Alias: change.Nacos.ResourceAlias, Connection: connection.Name, Endpoint: connection.Endpoint, DestinationDigest: sqlDestination(connection), ChangeDigest: digestJSON([]any{change.Service, change.Nacos}), DataID: change.Nacos.DataID, Group: change.Nacos.Group, Namespace: connection.Config["namespace"], Format: change.Nacos.Format, BeforeMD5: nacosMD5(before), AfterMD5: nacosMD5(after), Diff: diff}
		item.Restore = change.Nacos.Restore != nil
		target := nacosTargetKey(item)
		if seen[target] {
			return nil, fmt.Errorf("同一配置文档只能有一项变更，请合并配置项后提交")
		}
		seen[target] = true
		item.BeforeEncrypted, err = s.encryptSecret(before)
		if err != nil {
			return nil, err
		}
		item.AfterEncrypted, err = s.encryptSecret(after)
		if err != nil {
			return nil, err
		}
		var count int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM nacos_execution_ledger WHERE target_key=? AND change_id=?`, target, id).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, fmt.Errorf("Nacos 变更 #%d 已执行或结果待核对，不允许重复执行", id)
		}
		p.Configs = append(p.Configs, item)
	}
	return p, nil
}

func nacosInverseEntries(diffs []NacosDiff) []NacosEntry {
	entries := []NacosEntry{}
	for _, d := range diffs {
		e := NacosEntry{Key: d.Key, Action: "delete"}
		if d.Before != nil {
			e.Action, e.Value = "set", *d.Before
		}
		entries = append(entries, e)
	}
	return entries
}
func (s *Store) nacosRestoreSource(ref *NacosRestore) (*NacosConfigPlan, string, error) {
	run := s.DeliveryRun(ref.RunID)
	if run != nil {
		for _, p := range run.Plan.NacosPlans {
			for i := range p.Configs {
				item := &p.Configs[i]
				if item.ChangeID == ref.ChangeID {
					var status string
					if s.db.QueryRow(`SELECT status FROM nacos_execution_ledger WHERE target_key=? AND change_id=? AND run_id=?`, nacosTargetKey(*item), item.ChangeID, run.ID).Scan(&status) == nil && status == "success" {
						return item, run.Plan.Environment.Name, nil
					}
				}
			}
		}
	}
	return nil, "", fmt.Errorf("恢复来源不存在或不是已成功执行的 Nacos 配置")
}

// Approval snapshots contain encrypted before/after documents. The run revision
// reserves the node, and the durable ledger reserves each change. No retry on
// timeout: the server may already have applied a publish. CAS protects against
// a concurrent console/platform edit between the final read and publish.
func (s *Store) executeNacosNode(ctx context.Context, run *DeliveryRun, p *NacosExecutionPlan) (string, error) {
	clients := []*nacosClient{}
	for _, item := range p.Configs {
		change := s.GetChange(item.ChangeID)
		if change == nil || change.Status != ChangeReady || digestJSON([]any{change.Service, change.Nacos}) != item.ChangeDigest {
			return "", fmt.Errorf("Nacos 变更内容或状态变化，请重新审核")
		}
		c, err := s.nacosBinding(item.Service, run.Plan.Environment.Name, item.Alias)
		if err != nil || c.Name != item.Connection || sqlDestination(c) != item.DestinationDigest {
			return "", fmt.Errorf("Nacos 连接身份或环境绑定变化，请重新审核")
		}
		n, err := newNacosClient(ctx, c)
		if err != nil {
			return "", err
		}
		before, err := n.read(ctx, item.DataID, item.Group)
		if err != nil {
			return "", err
		}
		if nacosMD5(before) != item.BeforeMD5 {
			return "", fmt.Errorf("审批后 Nacos 配置已变化，禁止覆盖，请重新预览审核")
		}
		clients = append(clients, n)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	for _, item := range p.Configs {
		_, err = tx.Exec(`INSERT INTO nacos_execution_ledger(target_key,change_id,run_id,node_id,status,detail) VALUES(?,?,?,?,'running','已保留执行权，中断时必须核对远端结果')`, nacosTargetKey(item), item.ChangeID, run.ID, p.NodeID)
		if err != nil {
			return "", fmt.Errorf("Nacos 变更已执行或执行结果待核对，禁止重复提交")
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	for i, item := range p.Configs {
		after, err := s.decryptSecret(item.AfterEncrypted)
		if err == nil && nacosMD5(after) != item.AfterMD5 {
			err = fmt.Errorf("配置快照摘要不一致")
		}
		status, detail := "success", "配置已发布并核对摘要"
		if err == nil && item.BeforeMD5 != item.AfterMD5 {
			err = clients[i].publish(ctx, item.DataID, item.Group, item.Format, after, item.BeforeMD5)
			if err == nil {
				var actual string
				actual, err = clients[i].read(ctx, item.DataID, item.Group)
				if err == nil && nacosMD5(actual) != item.AfterMD5 {
					err = fmt.Errorf("发布后配置摘要不匹配，请核对远端结果")
				}
			}
		}
		if err != nil {
			status, detail = "uncertain", err.Error()+"；不自动重试，之前成功的配置不自动恢复，后续项未执行"
		}
		if _, e := s.db.Exec(`UPDATE nacos_execution_ledger SET status=?,detail=? WHERE target_key=? AND change_id=? AND run_id=?`, status, detail, nacosTargetKey(item), item.ChangeID, run.ID); e != nil {
			return "配置执行结果保存失败，请人工核对，禁止重复执行", e
		}
		if err != nil {
			return detail, err
		}
	}
	return fmt.Sprintf("Nacos 配置 %d 项完成；发布前基线与发布后摘要已校验", len(p.Configs)), nil
}

type NacosLedgerEntry struct {
	ChangeID int64  `json:"change_id"`
	RunID    int64  `json:"run_id"`
	NodeID   string `json:"node_id"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

func (s *Store) nacosLedger(runID int64) []NacosLedgerEntry {
	out := []NacosLedgerEntry{}
	query := `SELECT change_id,run_id,node_id,status,detail FROM nacos_execution_ledger`
	args := []any{}
	if runID > 0 {
		query += ` WHERE run_id=?`
		args = append(args, runID)
	}
	rows, err := s.db.Query(query+` ORDER BY run_id DESC,change_id DESC LIMIT 1000`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var item NacosLedgerEntry
		if rows.Scan(&item.ChangeID, &item.RunID, &item.NodeID, &item.Status, &item.Detail) == nil {
			out = append(out, item)
		}
	}
	return out
}

func registerNacosRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/nacos-executions", func(w http.ResponseWriter, r *http.Request) {
		runID, err := strconv.ParseInt(r.URL.Query().Get("run_id"), 10, 64)
		if r.URL.Query().Get("run_id") != "" && (err != nil || runID <= 0) {
			writeIntegrationJSON(w, nil, fmt.Errorf("执行单编号无效"))
			return
		}
		writeIntegrationJSON(w, s.nacosLedger(runID), nil)
	})
	mux.HandleFunc("POST /api/changes/{id}/nacos-preview", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Environment string `json:"environment"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
			writeIntegrationJSON(w, nil, fmt.Errorf("预览参数无效"))
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		plan, err := s.prepareNacosNode(ctx, "preview", []int64{id}, req.Environment)
		if plan != nil {
			plan = publicNacosPlans([]*NacosExecutionPlan{plan})[0]
		}
		writeIntegrationJSON(w, plan, err)
	})
	mux.HandleFunc("POST /api/delivery/runs/{id}/nacos-restore-draft", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChangeID int64 `json:"change_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
			writeIntegrationJSON(w, nil, fmt.Errorf("恢复参数无效"))
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		run := s.DeliveryRun(id)
		if run == nil {
			writeIntegrationJSON(w, nil, fmt.Errorf("执行单不存在"))
			return
		}
		actor := strings.TrimSpace(r.Header.Get("X-User"))
		if actor == "" {
			actor = "local"
		}
		if actor != "local" && actor != run.Owner && r.Header.Get("X-Role") != "admin" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "仅发起人或管理员可生成恢复草稿"})
			return
		}
		var chosen *NacosConfigPlan
		for _, p := range run.Plan.NacosPlans {
			for i := range p.Configs {
				if p.Configs[i].ChangeID == req.ChangeID {
					chosen = &p.Configs[i]
				}
			}
		}
		if chosen == nil {
			writeIntegrationJSON(w, nil, fmt.Errorf("执行单不包含该配置变更"))
			return
		}
		var status string
		if s.db.QueryRow(`SELECT status FROM nacos_execution_ledger WHERE target_key=? AND change_id=? AND run_id=?`, nacosTargetKey(*chosen), req.ChangeID, id).Scan(&status) != nil || status != "success" {
			writeIntegrationJSON(w, nil, fmt.Errorf("只有成功执行的配置可以生成恢复草稿，其他结果需人工核对"))
			return
		}
		original := s.GetChange(req.ChangeID)
		if original == nil || original.Nacos == nil || digestJSON([]any{original.Service, original.Nacos}) != chosen.ChangeDigest {
			writeIntegrationJSON(w, nil, fmt.Errorf("原变更记录已变化，不能生成恢复草稿"))
			return
		}
		next := &Change{Kind: "nacos", Title: "恢复：" + original.Title, Service: chosen.Service, Owner: actor, Status: ChangeDraft, RiskLevel: "high", RollbackPlan: "恢复失败时停止并人工核对", Nacos: &NacosChange{ResourceAlias: chosen.Alias, DataID: chosen.DataID, Group: chosen.Group, Format: chosen.Format, Entries: nacosInverseEntries(chosen.Diff), Restore: &NacosRestore{RunID: run.ID, ChangeID: chosen.ChangeID}}}
		if len(next.Nacos.Entries) == 0 {
			writeIntegrationJSON(w, nil, fmt.Errorf("没有实际差异，无需恢复"))
			return
		}
		err := s.CreateChange(next)
		if err == nil {
			s.Audit(actor, "create-nacos-restore", "delivery-run", fmt.Sprint(id), map[string]any{"change": next.ID, "source_change": req.ChangeID})
		}
		writeIntegrationJSON(w, next, err)
	})
}
