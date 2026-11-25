package deploy

import (
	"context"
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
	Protocol       string   `json:"protocol"`
	SnapshotID     int64    `json:"snapshot_id"`
	ApplicationID  string   `json:"application_id"`
	Service        string   `json:"service"`
	DisplayName    string   `json:"display_name"`
	Environment    string   `json:"environment"`
	CreatedAt      int64    `json:"created_at"`
	ID             string   `json:"id"`
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	Summary        string   `json:"summary"`
	Maintainer     string   `json:"maintainer,omitempty"`
	MaintainerName string   `json:"maintainer_name,omitempty"`
	Tags           []string `json:"tags"`
	SSE            bool     `json:"sse"`
}
type documentSearchResult struct {
	Items     []documentSearchItem `json:"items"`
	Total     int                  `json:"total"`
	Page      int                  `json:"page"`
	PageSize  int                  `json:"page_size"`
	Groups    []string             `json:"groups"`
	Snapshots int                  `json:"snapshots"`
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
		names := personnames.FromRequest(r)
		if utf8.RuneCountInString(keyword) > 256 || utf8.RuneCountInString(group) > 256 || utf8.RuneCountInString(maintainer) > 256 {
			fail(w, 400, "检索条件过长")
			return
		}
		result := documentSearchResult{Items: []documentSearchItem{}, Groups: []string{}, Page: 1, PageSize: 15}
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
		query := `SELECT v.id,v.application_id,v.service,COALESCE(NULLIF(s.display_name,''),s.name),v.environment,v.created_at,v.document,COALESCE(m.maintainer,''),COALESCE(om.document,'')
 FROM api_document_versions v JOIN services s ON s.application_id=v.application_id AND s.name=v.service
 LEFT JOIN api_document_maintainers m ON m.snapshot_id=v.id
 LEFT JOIN api_document_maintainer_maps om ON om.snapshot_id=v.id
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
		rows, e := st.db.QueryContext(ctx, query, args...)
		if e != nil {
			fail(w, 500, "文档检索失败，请重试")
			return
		}
		defer rows.Close()
		terms := strings.Fields(strings.ToLower(keyword))
		tags := map[string]bool{}
		offset := (result.Page - 1) * result.PageSize
		for rows.Next() {
			var base documentSearchItem
			var raw, fallback, ownerRaw string
			if e = rows.Scan(&base.SnapshotID, &base.ApplicationID, &base.Service, &base.DisplayName, &base.Environment, &base.CreatedAt, &raw, &fallback, &ownerRaw); e != nil {
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
			result.Snapshots++
			sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
			for _, op := range ops {
				if ctx.Err() != nil {
					fail(w, 500, "文档检索超时，请缩小范围后重试")
					return
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
				for _, tag := range op.Tags {
					tags[tag] = true
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
				if result.Total >= offset && len(result.Items) < result.PageSize {
					item := base
					item.Protocol = protocol
					item.ID = op.ID
					item.Method = op.Method
					item.Path = op.Path
					item.Summary = op.Summary
					item.Maintainer = op.Maintainer
					item.MaintainerName = maintainerName
					item.Tags = op.Tags
					item.SSE = op.SSE
					result.Items = append(result.Items, item)
				}
				result.Total++
			}
		}
		if rows.Err() != nil {
			fail(w, 500, "文档检索失败，请重试")
			return
		}
		for tag := range tags {
			result.Groups = append(result.Groups, tag)
		}
		sort.Strings(result.Groups)
		wj(w, result)
	})
}
