package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Source codes and labels are distinct; unknown/custom codes are never
// rewritten to a standard value. DB-native reading is a separately recorded TODO.
type ResearchIssueFields struct {
	SourceClosedAt  string            `json:"source_closed_at,omitempty"`
	SourceProductID string            `json:"source_product_id,omitempty"`
	PersonLabels    map[string]string `json:"person_labels,omitempty"`
	ProjectID       string            `json:"project_id,omitempty"`
	Severity        string            `json:"severity"`
	Priority        string            `json:"priority"`
	Resolution      string            `json:"resolution"`
	SeverityLabel   string            `json:"severity_label,omitempty"`
	PriorityLabel   string            `json:"priority_label,omitempty"`
	ResolutionLabel string            `json:"resolution_label,omitempty"`
	ReadAt          int64             `json:"source_fields_read_at"`
}

func issuePersonLabels(users map[string]string, accounts ...string) map[string]string {
	out := map[string]string{}
	for _, account := range accounts {
		if account != "" && !strings.EqualFold(account, "closed") && users[account] != "" {
			out[account] = users[account]
		}
	}
	return out
}

type sourceCode string

func (c *sourceCode) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		*c = ""
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*c = sourceCode(text)
		return nil
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		*c = sourceCode(number.String())
		return nil
	}
	return fmt.Errorf("来源枚举不是字符串或数字")
}

func sourceLabels(c *ResourceConnection, key string) map[string]string {
	out := map[string]string{}
	if c != nil {
		if json.Unmarshal([]byte(c.Config[key+"_labels"]), &out) != nil {
			return map[string]string{}
		}
	}
	return out
}

func applyResolutionOptions(c *ResourceConnection, remote *ResearchRemote) {
	if remote.Resolutions == nil {
		remote.Resolutions = map[string]string{}
	}
	for code, label := range remote.ResolutionList {
		if code != "" && label != "" {
			remote.Resolutions[code] = label
		}
	}
	for code, label := range sourceLabels(c, "resolution") {
		if code != "" && label != "" {
			remote.Resolutions[code] = label
		}
	}
	if len(remote.Resolutions) == 0 {
		remote.Resolutions = map[string]string{"fixed": "已修复", "duplicate": "重复 Bug", "notrepro": "无法重现", "bydesign": "设计如此", "external": "外部原因", "postponed": "延期处理", "willnotfix": "不予解决"}
	}
}

func registerResearchDictionaryRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("PUT /api/research/connections/{name}/field-labels", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Role") != "admin" {
			deliveryWriteError(w, 403, "仅管理员可维护来源字典")
			return
		}
		var maps map[string]map[string]string
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&maps) != nil {
			deliveryWriteError(w, 400, "字典格式无效")
			return
		}
		c := s.GetConnection(r.PathValue("name"))
		if c == nil || c.Kind != "zentao" {
			deliveryWriteError(w, 404, "来源连接不存在")
			return
		}
		if c.Config == nil {
			c.Config = map[string]string{}
		}
		for key, labels := range maps {
			if key != "severity" && key != "priority" && key != "resolution" {
				deliveryWriteError(w, 400, "不支持的来源字段")
				return
			}
			if len(labels) > 100 {
				deliveryWriteError(w, 400, "字典过大")
				return
			}
			for code, label := range labels {
				if code == "" || len(code) > 64 || strings.TrimSpace(label) == "" || len(label) > 256 || strings.ContainsAny(code, "\x00\r\n") {
					deliveryWriteError(w, 400, "字典键或显示名称无效")
					return
				}
			}
			raw, _ := json.Marshal(labels)
			c.Config[key+"_labels"] = string(raw)
		}
		if err := s.UpsertConnection(*c); err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		s.Audit(deliveryActor(r), "source-field-labels", "connection", c.Name, nil)
		deliveryJSON(w, map[string]bool{"ok": true})
	})
}
func issueFields(c *ResourceConnection, severity, priority, resolution string, closedDate ...string) ResearchIssueFields {
	fields := ResearchIssueFields{Severity: severity, Priority: priority, Resolution: resolution, SeverityLabel: sourceLabels(c, "severity")[severity], PriorityLabel: sourceLabels(c, "priority")[priority], ResolutionLabel: sourceLabels(c, "resolution")[resolution], ReadAt: time.Now().Unix()}
	if len(closedDate) > 0 {
		fields.SourceClosedAt = normalizedSourceDate(closedDate[0])
	}
	return fields
}

// Source timestamps without offsets describe the source's calendar date. Do
// not reinterpret them using the deployment host timezone or platform sync time.
func normalizedSourceDate(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02", time.RFC3339Nano} {
		if value, err := time.Parse(layout, raw); err == nil && value.Year() > 0 {
			return value.Format("2006-01-02 15:04:05")
		}
	}
	return ""
}
func saveIssueFields(tx *deployTx, id int64, fields ResearchIssueFields) error {
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO research_issue_fields(work_item_id,document) VALUES(?,?) ON CONFLICT(work_item_id) DO UPDATE SET document=excluded.document`, id, string(raw))
	return err
}

func registerResearchFieldRoutes(mux *http.ServeMux, s *Store) {
	// Explicit bounded read using the existing sync connection. This is NOT
	// database direct-read and never uses the sync identity for a Bug write.
	mux.HandleFunc("POST /api/research/refresh-fields", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			IDs []int64 `json:"ids"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request) != nil || len(request.IDs) == 0 || len(request.IDs) > 20 {
			deliveryWriteError(w, 400, "每次仅更新当前页，最多 20 项")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		seen := map[int64]bool{}
		clients := map[string]*zentaoLegacy{}
		values := map[int64]ResearchIssueFields{}
		for _, id := range request.IDs {
			if id <= 0 || seen[id] {
				deliveryWriteError(w, 400, "工作项编号重复")
				return
			}
			seen[id] = true
		}
		for _, id := range request.IDs {
			item := s.GetWorkItem(id)
			if item == nil {
				deliveryWriteError(w, 404, "工作项不存在")
				return
			}
			if !strings.HasPrefix(item.Source, "zentao:") {
				continue
			}
			c, err := s.researchConnection(item)
			if err != nil {
				deliveryWriteError(w, 400, err)
				return
			}
			var mapped int64
			if s.db.QueryRow(`SELECT work_item_id FROM external_work_items WHERE connection=? AND kind='bug' AND external_id=?`, c.Name, item.ExternalID).Scan(&mapped) != nil || mapped != id {
				deliveryWriteError(w, 400, "仅可读取已同步登记的来源 Bug；不接受手工伪造来源编号")
				return
			}
			client := clients[c.Name]
			if client == nil {
				client, err = s.connectionZentao(ctx, c)
				if err != nil {
					deliveryWriteError(w, 502, err)
					return
				}
				clients[c.Name] = client
			}
			remote, err := client.researchRead(ctx, item.ExternalID, "view")
			if err != nil {
				deliveryWriteError(w, 502, err)
				return
			}
			fields := issueFields(c, researchValue(remote.Bug, "severity"), researchValue(remote.Bug, "pri"), researchValue(remote.Bug, "resolution"), researchValue(remote.Bug, "closedDate"))
			fields.ProjectID = researchValue(remote.Bug, "project")
			fields.SourceProductID = researchValue(remote.Bug, "product")
			values[id] = fields
		}
		tx, err := s.db.Begin()
		if err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		defer tx.Rollback()
		for id, fields := range values {
			if err = saveIssueFields(tx, id, fields); err != nil {
				deliveryWriteError(w, 500, err)
				return
			}
		}
		if err = tx.Commit(); err != nil {
			deliveryWriteError(w, 500, err)
			return
		}
		s.Audit(deliveryActor(r), "refresh-source-fields", "work-items", "", map[string]any{"count": len(values)})
		deliveryJSON(w, map[string]int{"updated": len(values)})
	})
}
