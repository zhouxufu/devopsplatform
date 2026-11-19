package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"logplatform/pkg/personnames"
	"net/http"
	"sort"
	"strconv"
)

type DocumentOrigin struct {
	apidoc.Entity
	FirstSnapshot int64                `json:"first_snapshot"`
	IntroducedBy  *DocumentRequirement `json:"introduced_by,omitempty"`
	LastSnapshot  int64                `json:"last_snapshot"`
	LastWorkItem  *DocumentRequirement `json:"last_work_item,omitempty"`
}
type DocumentEvent struct {
	apidoc.Entity
	Change   string               `json:"change"`
	WorkItem *DocumentRequirement `json:"work_item,omitempty"`
	Before   json.RawMessage      `json:"before,omitempty"`
}
type DocumentLineage struct {
	Origins       map[string]DocumentOrigin `json:"origins"`
	Events        []DocumentEvent           `json:"events"`
	LegacyUnknown bool                      `json:"legacy_unknown"`
	AnalysisError string                    `json:"analysis_error,omitempty"`
}

func loadDocumentLineage(q interface{ QueryRow(string, ...any) *sql.Row }, id int64) (DocumentLineage, error) {
	v := DocumentLineage{Origins: map[string]DocumentOrigin{}, Events: []DocumentEvent{}}
	var raw string
	e := q.QueryRow(`SELECT document FROM api_document_lineage WHERE snapshot_id=?`, id).Scan(&raw)
	if e == sql.ErrNoRows {
		v.LegacyUnknown = id > 0
		return v, nil
	}
	if e != nil {
		return v, e
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return v, fmt.Errorf("接口来源记录无效")
	}
	return v, nil
}
func recordDocumentLineage(tx *deployTx, id, baseline int64, before, after []byte, context *DocumentChangeContext) error {
	previous, e := apidoc.DocumentEntities(before)
	if e != nil {
		return recordDocumentLineageUnavailable(tx, id, context, e)
	}
	current, e := apidoc.DocumentEntities(after)
	if e != nil {
		return recordDocumentLineageUnavailable(tx, id, context, e)
	}
	old, e := loadDocumentLineage(tx, baseline)
	if e != nil {
		return e
	}
	next := DocumentLineage{Origins: map[string]DocumentOrigin{}, Events: []DocumentEvent{}, LegacyUnknown: old.LegacyUnknown}
	keys := map[string]bool{}
	for k := range previous {
		keys[k] = true
	}
	for k := range current {
		keys[k] = true
	}
	sorted := []string{}
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	links := map[int64][]DocumentEvent{}
	definitionBudget := 1 << 20
	for _, key := range sorted {
		prev, existed := previous[key]
		cur, present := current[key]
		entity := cur
		if !present {
			entity = prev
		}
		origin, known := old.Origins[key]
		if !known {
			origin = DocumentOrigin{Entity: prev}
		}
		var ref *DocumentRequirement
		if context != nil {
			ref = context.WorkItem
			if entity.Field != nil {
				if f, ok := context.Fields[entity.Field.Pointer]; ok {
					copy := f
					ref = &copy
				}
			}
		}
		change := ""
		if !existed {
			change = "added"
		} else if !present {
			change = "removed"
		} else if prev.Digest != cur.Digest {
			change = "modified"
		}
		if present {
			if !existed {
				origin = DocumentOrigin{FirstSnapshot: id, IntroducedBy: ref}
			}
			origin.Entity = cur
			origin.Definition = nil // Current provenance needs identity, not duplicate schemas.
			if change != "" {
				origin.LastSnapshot = id
				origin.LastWorkItem = ref
			}
			next.Origins[key] = origin
		}
		if change != "" {
			event := DocumentEvent{Entity: entity, Change: change, WorkItem: ref, Before: prev.Definition}
			if len(event.Definition)+len(event.Before) > definitionBudget {
				event.Definition = nil
				event.Before = nil
			} else {
				definitionBudget -= len(event.Definition) + len(event.Before)
			}
			next.Events = append(next.Events, event)
			if ref != nil {
				links[ref.ID] = append(links[ref.ID], event)
			}
		}
	}
	raw, e := json.Marshal(next)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO api_document_lineage(snapshot_id,document) VALUES(?,?)`, id, string(raw)); e != nil {
		return e
	}
	for workID, events := range links {
		b, _ := json.Marshal(events)
		if _, e = tx.Exec(`INSERT INTO api_document_work_item_events(work_item_id,snapshot_id,document) VALUES(?,?,?)`, workID, id, string(b)); e != nil {
			return e
		}
	}
	return nil
}

// A bounded optional analysis must not prevent a valid full document import.
// Persist the explicit gap rather than claiming there were zero field changes.
func recordDocumentLineageUnavailable(tx *deployTx, id int64, context *DocumentChangeContext, cause error) error {
	line := DocumentLineage{Origins: map[string]DocumentOrigin{}, Events: []DocumentEvent{}, LegacyUnknown: true, AnalysisError: cause.Error()}
	raw, _ := json.Marshal(line)
	if _, e := tx.Exec(`INSERT INTO api_document_lineage(snapshot_id,document) VALUES(?,?)`, id, string(raw)); e != nil {
		return e
	}
	if context != nil && context.WorkItem != nil {
		events, _ := json.Marshal([]DocumentEvent{{Entity: apidoc.Entity{Key: "analysis", Kind: "analysis", Definition: json.RawMessage(`{"warning":"字段分析未完成，请核对完整版本差异"}`)}, Change: "unavailable", WorkItem: context.WorkItem}})
		_, e := tx.Exec(`INSERT INTO api_document_work_item_events(work_item_id,snapshot_id,document) VALUES(?,?,?)`, context.WorkItem.ID, id, string(events))
		return e
	}
	return nil
}
func registerDocumentTraceRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), service func(http.ResponseWriter, *http.Request) *Service) {
	mux.HandleFunc("GET /api/api-documents/{id}/operation", func(w http.ResponseWriter, r *http.Request) {
		sv := service(w, r)
		if sv == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v, e := st.apiDocumentSnapshot(sv.ApplicationID, id)
		if e != nil {
			fail(w, 404, "文档不存在")
			return
		}
		if r.URL.Query().Get("environment") != v.Environment {
			fail(w, 400, "文档环境不匹配")
			return
		}
		d, ops, _, e := apidoc.Parse(v.Document)
		if e != nil {
			fail(w, 400, e)
			return
		}
		owners, e := loadDocumentMaintainers(st.db, v.ID)
		if e != nil {
			fail(w, 500, e)
			return
		}
		if e = applyFrozenDocumentMaintainers(ops, owners, v.DefaultMaintainer); e != nil {
			fail(w, 500, e)
			return
		}
		names := personnames.FromRequest(r)
		for i := range ops {
			ops[i].MaintainerName = names.Name(ops[i].Maintainer)
		}
		for _, op := range ops {
			if op.ID == r.URL.Query().Get("operation") {
				lineage, e := loadDocumentLineage(st.db, id)
				if e != nil {
					fail(w, 500, e)
					return
				}
				for key, origin := range lineage.Origins {
					if origin.Operation != op.ID {
						delete(lineage.Origins, key)
					}
				}
				events := []DocumentEvent{}
				for _, event := range lineage.Events {
					if event.Operation == op.ID {
						events = append(events, event)
					}
				}
				lineage.Events = events
				var root map[string]json.RawMessage
				_ = json.Unmarshal(v.Document, &root)
				wj(w, map[string]any{"id": id, "service": sv.Name, "environment": v.Environment, "digest": v.Digest, "operation": op, "components": d.Components, "path_parameters": d.Paths[op.Path]["parameters"], "servers": root["servers"], "security": root["security"], "lineage": lineage})
				return
			}
		}
		fail(w, 404, "接口不存在")
	})
	mux.HandleFunc("GET /api/api-documents/work-item-changes", func(w http.ResponseWriter, r *http.Request) {
		workID, _ := strconv.ParseInt(r.URL.Query().Get("work_item_id"), 10, 64)
		if workID <= 0 || st.GetWorkItem(workID) == nil {
			fail(w, 404, "工作项不存在")
			return
		}
		query := `SELECT v.id,v.baseline_id,v.service,v.environment,v.created_at,e.document FROM api_document_work_item_events e JOIN api_document_versions v ON v.id=e.snapshot_id WHERE e.work_item_id=?`
		args := []any{workID}
		if r.URL.Query().Get("service") != "" {
			sv := service(w, r)
			if sv == nil {
				return
			}
			query += ` AND v.application_id=?`
			args = append(args, sv.ApplicationID)
		}
		if env := r.URL.Query().Get("environment"); env != "" {
			query += ` AND v.environment=?`
			args = append(args, env)
		}
		if before, _ := strconv.ParseInt(r.URL.Query().Get("before_id"), 10, 64); before > 0 {
			query += ` AND v.id<?`
			args = append(args, before)
		}
		rows, e := st.db.Query(query+` ORDER BY v.id DESC LIMIT 51`, args...)
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		var next int64
		for rows.Next() {
			var id, base, at int64
			var sv, env, raw string
			if e = rows.Scan(&id, &base, &sv, &env, &at, &raw); e != nil {
				fail(w, 500, e)
				return
			}
			if len(items) == 50 {
				next = items[49]["snapshot_id"].(int64)
				break
			}
			var events []DocumentEvent
			if json.Unmarshal([]byte(raw), &events) != nil {
				fail(w, 500, "接口变更记录无效")
				return
			}
			items = append(items, map[string]any{"snapshot_id": id, "baseline_id": base, "service": sv, "environment": env, "created_at": at, "events": events})
		}
		if e = rows.Err(); e != nil {
			fail(w, 500, e)
			return
		}
		wj(w, map[string]any{"items": items, "next_before_id": next, "historical_coverage": "记录启用后的修订；历史未关联来源不推断补写"})
	})
}
