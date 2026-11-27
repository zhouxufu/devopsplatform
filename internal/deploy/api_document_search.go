package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/personnames"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"logplatform/pkg/apidoc"
)

type documentSearchItem struct {
	Update         DocumentOperationUpdate `json:"update"`
	Protocol       string                  `json:"protocol"`
	SnapshotID     int64                   `json:"snapshot_id"`
	ApplicationID  string                  `json:"application_id"`
	Service        string                  `json:"service"`
	DisplayName    string                  `json:"display_name"`
	Environment    string                  `json:"environment"`
	CreatedAt      int64                   `json:"created_at"`
	ID             string                  `json:"id"`
	Method         string                  `json:"method"`
	Path           string                  `json:"path"`
	Summary        string                  `json:"summary"`
	Maintainer     string                  `json:"maintainer,omitempty"`
	MaintainerName string                  `json:"maintainer_name,omitempty"`
	Tags           []string                `json:"tags"`
	SSE            bool                    `json:"sse"`
}
type documentSearchResult struct {
	Items     []documentSearchItem `json:"items"`
	Total     int                  `json:"total"`
	Page      int                  `json:"page"`
	PageSize  int                  `json:"page_size"`
	Groups    []string             `json:"groups"`
	Snapshots int                  `json:"snapshots"`
}

type documentSearchOperation struct {
	ApplicationID string
	Environment   string
	ID            string
}

func (st *Store) documentWorkItemOperations(ctx context.Context, workID int64, protocol, service, environment string, hasEnvironment bool) (map[documentSearchOperation]int64, error) {
	query := `SELECT v.application_id,v.environment,v.id,e.document
 FROM api_document_work_item_events e JOIN api_document_versions v ON v.id=e.snapshot_id
 LEFT JOIN api_document_protocols p ON p.snapshot_id=v.id
 WHERE e.work_item_id=? AND COALESCE(p.protocol,'http')=?`
	args := []any{workID, protocol}
	if service != "" {
		query += ` AND v.service=?`
		args = append(args, service)
	}
	if hasEnvironment {
		query += ` AND v.environment=?`
		args = append(args, environment)
	}
	rows, err := st.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matches := map[documentSearchOperation]int64{}
	addEvents := func(key documentSearchOperation, snapshot int64, events []DocumentEvent) {
		for _, event := range events {
			if event.Operation == "" || event.Change == "unavailable" {
				continue
			}
			key.ID = event.Operation
			if first := matches[key]; first == 0 || snapshot < first {
				matches[key] = snapshot
			}
		}
	}
	for rows.Next() {
		var key documentSearchOperation
		var snapshot int64
		var raw string
		if err = rows.Scan(&key.ApplicationID, &key.Environment, &snapshot, &raw); err != nil {
			return nil, err
		}
		var events []DocumentEvent
		if err = json.Unmarshal([]byte(raw), &events); err != nil {
			return nil, err
		}
		addEvents(key, snapshot, events)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	query = `SELECT v.application_id,v.environment,v.id,l.document,d.document
 FROM api_document_report_links l JOIN api_document_reports r ON r.id=l.report_id
 JOIN api_document_versions v ON v.id=r.snapshot_id AND v.baseline_id=r.baseline_id
 JOIN api_document_lineage d ON d.snapshot_id=v.id
 WHERE l.source='manual' AND r.state='saved' AND r.protocol=? AND l.document LIKE ?`
	args = []any{protocol, `%"id":` + strconv.FormatInt(workID, 10) + `%`}
	if service != "" {
		query += ` AND v.service=?`
		args = append(args, service)
	}
	if hasEnvironment {
		query += ` AND v.environment=?`
		args = append(args, environment)
	}
	reports, err := st.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer reports.Close()
	for reports.Next() {
		var key documentSearchOperation
		var snapshot int64
		var linksRaw, lineageRaw string
		if err = reports.Scan(&key.ApplicationID, &key.Environment, &snapshot, &linksRaw, &lineageRaw); err != nil {
			return nil, err
		}
		var links []DocumentRequirement
		if err = json.Unmarshal([]byte(linksRaw), &links); err != nil {
			return nil, err
		}
		for _, link := range links {
			if link.ID != workID {
				continue
			}
			var lineage struct {
				Events []DocumentEvent `json:"events"`
			}
			if err = json.Unmarshal([]byte(lineageRaw), &lineage); err != nil {
				return nil, err
			}
			addEvents(key, snapshot, lineage.Events)
			break
		}
	}
	return matches, reports.Err()
}

// Search only the latest immutable snapshot in each existing application and
// environment. Stream documents one at a time; retain just the requested page,
// not the schemas or request examples of every application in the response.
func registerDocumentSearchRoute(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("GET /api/api-documents/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		protocol := q.Get("protocol")
		if protocol == "" {
			protocol = "http"
		}
		if protocol != "http" && protocol != "dubbo" {
			fail(w, 400, "文档协议无效")
			return
		}
		keyword := strings.TrimSpace(q.Get("q"))
		group := q.Get("group")
		maintainer := strings.ToLower(strings.TrimSpace(q.Get("maintainer")))
		var workID int64
		if value := q.Get("work_item_id"); value != "" {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				fail(w, 400, "请选择有效的需求或 Bug")
				return
			}
			workID = id
		}
		names := personnames.FromRequest(r)
		if utf8.RuneCountInString(keyword) > 256 || utf8.RuneCountInString(group) > 256 || utf8.RuneCountInString(maintainer) > 256 {
			fail(w, 400, "检索条件过长")
			return
		}
		result := documentSearchResult{Items: []documentSearchItem{}, Groups: []string{}, Page: 1, PageSize: 20}
		for key, target := range map[string]*int{"page": &result.Page, "page_size": &result.PageSize} {
			if q.Has(key) {
				v, e := strconv.Atoi(q.Get(key))
				if e != nil || v < 1 || key == "page" && v > 100000 || key == "page_size" && v > 100 {
					fail(w, 400, "分页参数无效")
					return
				}
				*target = v
			}
		}
		query := `SELECT v.id,v.application_id,v.service,COALESCE(NULLIF(s.display_name,''),s.name),v.environment,v.created_at,v.document,COALESCE(m.maintainer,''),COALESCE(om.document,''),COALESCE(lineage.document,'{}'),COALESCE(updates.document,'')
 FROM api_document_versions v JOIN services s ON s.application_id=v.application_id AND s.name=v.service
 LEFT JOIN api_document_maintainers m ON m.snapshot_id=v.id
 LEFT JOIN api_document_maintainer_maps om ON om.snapshot_id=v.id
 LEFT JOIN api_document_lineage lineage ON lineage.snapshot_id=v.id
 LEFT JOIN api_document_operation_updates updates ON updates.snapshot_id=v.id
 WHERE v.id IN (SELECT MAX(api_document_versions.id) FROM api_document_versions LEFT JOIN api_document_protocols dp ON dp.snapshot_id=api_document_versions.id WHERE COALESCE(dp.protocol,'http')=? GROUP BY application_id,environment)`
		args := []any{protocol}
		if name := q.Get("service"); name != "" {
			if st.GetService(name) == nil {
				fail(w, 400, "应用不存在")
				return
			}
			query += ` AND v.service=?`
			args = append(args, name)
		}
		if q.Has("environment") {
			env := q.Get("environment")
			if env != "" && st.GetEnvironment(env) == nil {
				fail(w, 400, "文档环境不存在")
				return
			}
			query += ` AND v.environment=?`
			args = append(args, env)
		}
		query += ` ORDER BY v.service,v.environment,v.id`
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var workOperations map[documentSearchOperation]int64
		if workID > 0 {
			var err error
			workOperations, err = st.documentWorkItemOperations(ctx, workID, protocol, q.Get("service"), q.Get("environment"), q.Has("environment"))
			if err != nil {
				fail(w, 500, "读取需求关联的接口失败，请重试")
				return
			}
		}
		rows, e := st.db.QueryContext(ctx, query, args...)
		if e != nil {
			fail(w, 500, "文档检索失败，请重试")
			return
		}
		defer rows.Close()
		terms := strings.Fields(strings.ToLower(keyword))
		tags := map[string]bool{}
		offset := (result.Page - 1) * result.PageSize
		if q.Get("sort") == "updated" && offset > 10000 {
			fail(w, 400, "最近变更排序最多浏览一万项，请缩小应用或环境范围")
			return
		}
		for rows.Next() {
			var base documentSearchItem
			var raw, fallback, ownerRaw, lineageRaw, updateRaw string
			if e = rows.Scan(&base.SnapshotID, &base.ApplicationID, &base.Service, &base.DisplayName, &base.Environment, &base.CreatedAt, &raw, &fallback, &ownerRaw, &lineageRaw, &updateRaw); e != nil {
				fail(w, 500, "文档检索失败，请重试")
				return
			}
			_, ops, _, err := apidoc.Parse([]byte(raw))
			if err != nil {
				fail(w, 500, fmt.Sprintf("文档快照 #%d 无法读取，请核对文档后重试", base.SnapshotID))
				return
			}
			owners, err := decodeDocumentMaintainers(ownerRaw)
			if err != nil {
				fail(w, 500, err)
				return
			}
			if err = applyFrozenDocumentMaintainers(ops, owners, fallback); err != nil {
				fail(w, 500, err)
				return
			}
			updates := decodeOperationUpdates(lineageRaw, base.SnapshotID)
			if updateRaw != "" {
				if e = json.Unmarshal([]byte(updateRaw), &updates); e != nil {
					fail(w, 500, "接口更新记录无法读取")
					return
				}
			}
			result.Snapshots++
			sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
			for _, op := range ops {
				if ctx.Err() != nil {
					fail(w, 500, "文档检索超时，请缩小范围后重试")
					return
				}
				for _, tag := range op.Tags {
					tags[tag] = true
				}
				if workID > 0 {
					first := workOperations[documentSearchOperation{base.ApplicationID, base.Environment, op.ID}]
					if first == 0 || first > base.SnapshotID {
						continue
					}
				}
				maintainerName := names.Name(op.Maintainer)
				if maintainer != "" && !strings.Contains(strings.ToLower(op.Maintainer+" "+maintainerName), maintainer) {
					continue
				}
				haystack := strings.ToLower(strings.Join([]string{op.Summary, op.Description, op.Path, op.Method, op.Maintainer, maintainerName, strings.Join(op.Tags, " ")}, " "))
				matches := true
				for _, term := range terms {
					if !strings.Contains(haystack, term) {
						matches = false
						break
					}
				}
				if !matches {
					continue
				}
				if group != "" {
					found := false
					for _, tag := range op.Tags {
						if tag == group {
							found = true
							break
						}
					}
					if !found {
						continue
					}
				}
				if q.Get("sort") == "updated" || result.Total >= offset && len(result.Items) < result.PageSize {
					item := base
					item.Protocol = protocol
					item.Update = updates[op.ID]
					item.ID = op.ID
					item.Method = op.Method
					item.Path = op.Path
					item.Summary = op.Summary
					item.Maintainer = op.Maintainer
					item.MaintainerName = maintainerName
					item.Tags = op.Tags
					item.SSE = op.SSE
					result.Items = append(result.Items, item)
					if q.Get("sort") == "updated" && len(result.Items) > offset+result.PageSize+1000 {
						sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].Update.SnapshotID > result.Items[j].Update.SnapshotID })
						result.Items = result.Items[:offset+result.PageSize]
					}
				}
				result.Total++
			}
		}
		if rows.Err() != nil {
			fail(w, 500, "文档检索失败，请重试")
			return
		}
		rows.Close()
		if q.Get("sort") == "updated" {
			sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].Update.SnapshotID > result.Items[j].Update.SnapshotID })
			end := offset + result.PageSize
			if end > len(result.Items) {
				end = len(result.Items)
			}
			if offset > end {
				offset = end
			}
			result.Items = result.Items[offset:end]
		}
		cache := map[int64]DocumentOperationUpdate{}
		for i := range result.Items {
			item := &result.Items[i]
			u, ok := cache[item.Update.SnapshotID]
			if !ok {
				u = st.completeOperationUpdate(item.ApplicationID, item.Environment, protocol, item.Update)
				cache[item.Update.SnapshotID] = u
			}
			u.Kind = item.Update.Kind
			item.Update = u
		}
		for tag := range tags {
			result.Groups = append(result.Groups, tag)
		}
		sort.Strings(result.Groups)
		wj(w, result)
	})
}
