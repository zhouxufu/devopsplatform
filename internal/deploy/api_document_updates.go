package deploy

import (
	"database/sql"
	"encoding/json"
	"logplatform/pkg/apidoc"
	"net/http"
	"strconv"
)

type DocumentOperationUpdate struct {
	SnapshotID int64  `json:"snapshot_id"`
	ChangedAt  int64  `json:"changed_at"`
	Kind       string `json:"kind"`
	ReportID   string `json:"report_id,omitempty"`
}

func recordOperationUpdates(tx *deployTx, id, baseline int64, ops []apidoc.Operation, diff apidoc.Comparison) error {
	previous := map[string]DocumentOperationUpdate{}
	var raw string
	e := tx.QueryRow(`SELECT document FROM api_document_operation_updates WHERE snapshot_id=?`, baseline).Scan(&raw)
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &previous); e != nil {
			return e
		}
	} else if e == sql.ErrNoRows {
		lineage, e := loadDocumentLineage(tx, baseline)
		if e != nil {
			return e
		}
		previous = documentOperationUpdates(lineage, baseline)
	} else {
		return e
	}
	changes := map[string]string{}
	for _, change := range diff.Changes {
		changes[change.ID] = change.Kind
	}
	// A truncated display comparison cannot establish complete endpoint history.
	if diff.Truncated {
		return recordUnknownOperationUpdates(tx, id, ops)
	}
	next := map[string]DocumentOperationUpdate{}
	for _, op := range ops {
		v := previous[op.ID]
		if kind := changes[op.ID]; kind != "" {
			v = DocumentOperationUpdate{SnapshotID: id, Kind: kind}
		}
		next[op.ID] = v
	}
	b, e := json.Marshal(next)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO api_document_operation_updates(snapshot_id,document) VALUES(?,?)`, id, string(b))
	return e
}

func recordUnknownOperationUpdates(tx *deployTx, id int64, ops []apidoc.Operation) error {
	// A truncated comparison cannot prove the time of omitted changes. Preserve
	// an explicit unknown value instead of stamping every endpoint with this upload.
	next := map[string]DocumentOperationUpdate{}
	for _, op := range ops {
		next[op.ID] = DocumentOperationUpdate{}
	}
	b, e := json.Marshal(next)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO api_document_operation_updates(snapshot_id,document) VALUES(?,?)`, id, string(b))
	return e
}

func documentOperationUpdates(lineage DocumentLineage, snapshot int64) map[string]DocumentOperationUpdate {
	out := map[string]DocumentOperationUpdate{}
	for _, origin := range lineage.Origins {
		if origin.LastSnapshot <= 0 {
			continue
		}
		value := out[origin.Operation]
		if origin.LastSnapshot > value.SnapshotID {
			value.SnapshotID = origin.LastSnapshot
			value.Kind = "modified"
			if origin.Field == nil && origin.FirstSnapshot == origin.LastSnapshot {
				value.Kind = "added"
			}
			out[origin.Operation] = value
		}
	}
	// Field additions must not turn an existing operation into an added operation.
	for _, origin := range lineage.Origins {
		if origin.Field == nil && origin.LastSnapshot > 0 {
			value := out[origin.Operation]
			if origin.FirstSnapshot == value.SnapshotID {
				value.Kind = "added"
			}
			out[origin.Operation] = value
		}
	}
	// Deleted shared fields no longer have an origin; the event still changes their parent operation.
	for _, event := range lineage.Events {
		if _, present := lineage.Origins["operation:"+event.Operation]; present && snapshot > out[event.Operation].SnapshotID {
			out[event.Operation] = DocumentOperationUpdate{SnapshotID: snapshot, Kind: "modified"}
		}
	}
	return out
}
func (s *Store) completeOperationUpdate(app, env, protocol string, v DocumentOperationUpdate) DocumentOperationUpdate {
	if v.SnapshotID <= 0 {
		return v
	}
	if e := s.db.QueryRow(`SELECT created_at FROM api_document_versions WHERE id=? AND application_id=? AND environment=? AND `+documentProtocolSQL+`=?`, v.SnapshotID, app, env, protocol).Scan(&v.ChangedAt); e != nil {
		return DocumentOperationUpdate{}
	}
	_ = s.db.QueryRow(`SELECT id FROM api_document_reports WHERE snapshot_id=? AND application_id=? AND environment=? AND protocol=? AND state='saved' ORDER BY sequence LIMIT 1`, v.SnapshotID, app, env, protocol).Scan(&v.ReportID)
	return v
}
func (s *Store) documentUpdateInfo(v APIDocumentSnapshot) map[string]any {
	var current int64
	_ = s.db.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND `+documentProtocolSQL+`=? ORDER BY id DESC LIMIT 1`, v.ApplicationID, v.Environment, v.Protocol).Scan(&current)
	lineage, e := loadDocumentLineage(s.db, v.ID)
	updates := map[string]DocumentOperationUpdate{}
	if e == nil {
		updates = documentOperationUpdates(lineage, v.ID)
	}
	var stored string
	if e := s.db.QueryRow(`SELECT document FROM api_document_operation_updates WHERE snapshot_id=?`, v.ID).Scan(&stored); e == nil {
		_ = json.Unmarshal([]byte(stored), &updates)
	}
	cache := map[int64]DocumentOperationUpdate{}
	for key, u := range updates {
		cached, ok := cache[u.SnapshotID]
		if !ok {
			cached = s.completeOperationUpdate(v.ApplicationID, v.Environment, v.Protocol, u)
			cache[u.SnapshotID] = cached
		}
		cached.Kind = u.Kind
		updates[key] = cached
	}
	var reportID string
	_ = s.db.QueryRow(`SELECT id FROM api_document_reports WHERE snapshot_id=? AND state='saved' ORDER BY sequence LIMIT 1`, v.ID).Scan(&reportID)
	last, e := scanDocumentReport(s.db.QueryRow(`SELECT `+reportSelectColumns+` FROM api_document_reports WHERE application_id=? AND environment=? AND protocol=? AND state IN ('saved','unchanged') ORDER BY updated_at DESC,sequence DESC LIMIT 1`, v.ApplicationID, v.Environment, v.Protocol))
	var recent any
	if e == nil {
		recent = s.publicDocumentReport(last)
	}
	return map[string]any{"current_id": current, "is_current": v.ID == current, "report_id": reportID, "operations": updates, "last_successful_report": recent}
}

func registerDocumentOperationHistory(mux *http.ServeMux, s *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("GET /api/api-documents/operation-history", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sv := s.GetService(q.Get("service"))
		if sv == nil {
			fail(w, 400, "请选择已有应用")
			return
		}
		protocol := q.Get("protocol")
		if protocol == "" {
			protocol = "http"
		}
		op := q.Get("operation")
		if op == "" || len(op) > 2048 {
			fail(w, 400, "接口标识无效")
			return
		}
		query := `SELECT ` + documentMetadataColumns + ` FROM api_document_versions WHERE application_id=? AND environment=? AND ` + documentProtocolSQL + `=?`
		args := []any{sv.ApplicationID, q.Get("environment"), protocol}
		if q.Get("before") != "" {
			id, e := strconv.ParseInt(q.Get("before"), 10, 64)
			if e != nil || id <= 0 {
				fail(w, 400, "历史游标无效")
				return
			}
			query += ` AND id<?`
			args = append(args, id)
		}
		rows, e := s.db.Query(query+` ORDER BY id DESC LIMIT 21`, args...)
		if e != nil {
			fail(w, 500, e)
			return
		}
		snaps := []APIDocumentSnapshot{}
		for rows.Next() {
			v, e := scanDocument(rows)
			if e != nil {
				rows.Close()
				fail(w, 500, e)
				return
			}
			snaps = append(snaps, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			fail(w, 500, e)
			return
		}
		hasMore := len(snaps) > 20
		if hasMore {
			snaps = snaps[:20]
		}
		items := []map[string]any{}
		var before int64
		for _, v := range snaps {
			before = v.ID
			lineage, e := loadDocumentLineage(s.db, v.ID)
			if e != nil && e != sql.ErrNoRows {
				fail(w, 500, e)
				return
			}
			events := []DocumentEvent{}
			for _, event := range lineage.Events {
				if event.Operation == op {
					events = append(events, event)
				}
			}
			_, present := lineage.Origins["operation:"+op]
			kind := ""
			var rawUpdates string
			knownUpdates := map[string]DocumentOperationUpdate{}
			if err := s.db.QueryRow(`SELECT document FROM api_document_operation_updates WHERE snapshot_id=?`, v.ID).Scan(&rawUpdates); err == nil {
				if json.Unmarshal([]byte(rawUpdates), &knownUpdates) == nil {
					if u, ok := knownUpdates[op]; ok {
						present = true
						if u.SnapshotID == v.ID {
							kind = u.Kind
						}
					}
				}
			}
			if len(events) == 0 && kind == "" && q.Get("include_unchanged") != "1" {
				continue
			}
			if len(events) == 0 && kind == "" && !present && !lineage.LegacyUnknown {
				continue
			}

			for i := range events {
				events[i].Before = nil
				events[i].Definition = nil
			}
			items = append(items, map[string]any{"snapshot": v, "events": events, "kind": kind, "unknown": lineage.LegacyUnknown || lineage.AnalysisError != "", "unchanged": len(events) == 0 && kind == ""})
		}
		wj(w, map[string]any{"items": items, "has_more": hasMore, "next_before": before})
	})
}

// Decode on the same stream as the document; query metadata only after closing
// the rows, so SQLite's single-connection mode cannot deadlock.
func decodeOperationUpdates(raw string, snapshot int64) map[string]DocumentOperationUpdate {
	var v DocumentLineage
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	return documentOperationUpdates(v, snapshot)
}
