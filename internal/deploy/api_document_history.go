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
	"time"
)

const documentProtocolSQL = `COALESCE((SELECT protocol FROM api_document_protocols WHERE snapshot_id=api_document_versions.id),'http')`
const documentMetadataColumns = `id,application_id,environment,service,run_id,artifact_id,artifact_digest,commit_ref,provider,provider_version,digest,created_at,baseline_id,comparison,'',COALESCE((SELECT maintainer FROM api_document_maintainers WHERE snapshot_id=api_document_versions.id),''),` + documentProtocolSQL
const documentColumns = `id,application_id,environment,service,run_id,artifact_id,artifact_digest,commit_ref,provider,provider_version,digest,created_at,baseline_id,comparison,document,COALESCE((SELECT maintainer FROM api_document_maintainers WHERE snapshot_id=api_document_versions.id),''),` + documentProtocolSQL

type documentScanner interface{ Scan(...any) error }

func scanDocument(row documentScanner) (APIDocumentSnapshot, error) {
	var v APIDocumentSnapshot
	var comparison, raw string
	e := row.Scan(&v.ID, &v.ApplicationID, &v.Environment, &v.Service, &v.RunID, &v.ArtifactID, &v.ArtifactDigest, &v.Commit, &v.Provider, &v.ProviderVersion, &v.Digest, &v.CreatedAt, &v.BaselineID, &comparison, &raw, &v.DefaultMaintainer, &v.Protocol)
	v.Document = json.RawMessage(raw)
	var diff apidoc.Comparison
	_ = json.Unmarshal([]byte(comparison), &diff)
	v.Changes = diff.Summary
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal([]byte(comparison), &metadata)
	v.Compared = metadata["summary"] != nil
	return v, e
}
func (s *Store) apiDocumentSnapshot(app string, id int64) (APIDocumentSnapshot, error) {
	return scanDocument(s.db.QueryRow(`SELECT `+documentColumns+` FROM api_document_versions WHERE application_id=? AND id=?`, app, id))
}
func (s *Store) recordAPIDocument(sv *Service, snap APIDocumentSnapshot, raw []byte, expectedBaseline ...int64) (int64, error) {
	if snap.ReportID == "" {
		protocol := "http"
		if d, _, _, e := apidoc.Parse(raw); e == nil {
			protocol = d.Protocol()
		}
		source := snap.ReportSource
		if source == "" {
			source = "build"
			if snap.Provider == "openapi-edit" {
				source = "edit"
			}
			if snap.Provider == "openapi-import" {
				source = "file"
			}
		}
		snap.ReportID = newDocumentReportID()
		now := time.Now().Unix()
		actor := snap.ReportActor
		if actor == "" && snap.RunID > 0 {
			if run := s.GetRun(snap.RunID); run != nil {
				actor = run.Actor
			}
		}
		if e := s.insertDocumentReport(DocumentReport{ID: snap.ReportID, ApplicationID: sv.ApplicationID, Service: sv.Name, Environment: snap.Environment, Protocol: protocol, Actor: actor, Source: source, State: "processing", CreatedAt: now, UpdatedAt: now, RunID: snap.RunID, Commit: snap.Commit}); e != nil {
			return 0, e
		}
	}
	id, e := s.recordAPIDocumentContent(sv, snap, raw, expectedBaseline...)
	if e != nil {
		state, message := "failed", "文档校验或保存未通过"
		if e == errDocumentCommitUnknown {
			state, message = "uncertain", "保存结果待核对，请查询原记录"
		}
		_, _ = s.db.Exec(`UPDATE api_document_reports SET state=?,error=?,updated_at=? WHERE id=? AND state='processing'`, state, message, time.Now().Unix(), snap.ReportID)
	}
	return id, e
}

var errDocumentCommitUnknown = fmt.Errorf("文档提交结果待核对，请查询原上报记录")

func (s *Store) recordAPIDocumentContent(sv *Service, snap APIDocumentSnapshot, raw []byte, expectedBaseline ...int64) (int64, error) {
	s.apiDocumentMu.Lock()
	defer s.apiDocumentMu.Unlock()
	parsed, newOperations, digest, e := apidoc.Parse(raw)
	if e != nil {
		return 0, e
	}
	if snap.Environment != "" && s.GetEnvironment(snap.Environment) == nil {
		return 0, fmt.Errorf("文档环境不存在")
	}
	protocol := parsed.Protocol()
	tx, e := s.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if snap.ReportID != "" {
		if _, e = tx.Exec(`UPDATE api_document_reports SET protocol=? WHERE id=? AND state='processing'`, protocol, snap.ReportID); e != nil {
			return 0, e
		}
	}
	if len(expectedBaseline) > 0 {
		var latest int64
		err := tx.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND `+documentProtocolSQL+`=? ORDER BY id DESC LIMIT 1`, sv.ApplicationID, snap.Environment, protocol).Scan(&latest)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if latest != expectedBaseline[0] {
			return 0, errDocumentBaselineChanged
		}
		var existing int64
		err = tx.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND digest=? ORDER BY id DESC LIMIT 1`, sv.ApplicationID, snap.Environment, digest).Scan(&existing)
		if err == nil && (snap.Provider != "openapi-edit" || existing == latest) {
			return finishDuplicateDocumentReport(tx, sv, snap, raw, protocol, existing)
		}
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
	}
	// File import has no build identity. Reuse matching immutable content even
	// when it originally came from a build; never promote a historical match.
	if len(expectedBaseline) == 0 && snap.Provider == "openapi-import" {
		var existing int64
		err := tx.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND digest=? ORDER BY id DESC LIMIT 1`, sv.ApplicationID, snap.Environment, digest).Scan(&existing)
		if err == nil {
			return finishDuplicateDocumentReport(tx, sv, snap, raw, protocol, existing)
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
	}
	var id int64
	e = tx.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND run_id=? AND artifact_id=? AND digest=?`, sv.ApplicationID, snap.Environment, snap.RunID, snap.ArtifactID, digest).Scan(&id)
	if e == nil && snap.Provider != "openapi-edit" {
		return finishDuplicateDocumentReport(tx, sv, snap, raw, protocol, id)
	}
	if e != nil && e != sql.ErrNoRows {
		return 0, e
	}
	var baseline int64
	var previous, previousDefault string
	e = tx.QueryRow(`SELECT id,document,COALESCE((SELECT maintainer FROM api_document_maintainers WHERE snapshot_id=api_document_versions.id),'') FROM api_document_versions WHERE application_id=? AND environment=? AND `+documentProtocolSQL+`=? ORDER BY id DESC LIMIT 1`, sv.ApplicationID, snap.Environment, protocol).Scan(&baseline, &previous, &previousDefault)
	if e != nil && e != sql.ErrNoRows {
		return 0, e
	}
	var oldOperations []apidoc.Operation
	if baseline > 0 {
		_, oldOperations, _, e = apidoc.Parse([]byte(previous))
		if e != nil {
			return 0, e
		}
		oldOwners, err := loadDocumentMaintainers(tx, baseline)
		if err != nil {
			return 0, err
		}
		if e = applyFrozenDocumentMaintainers(oldOperations, oldOwners, previousDefault); e != nil {
			return 0, e
		}
	}
	owners := nextDocumentMaintainers(newOperations, oldOperations, snap.DefaultMaintainer)
	if snap.ExpectedMaintainerDigest != "" && snap.ExpectedMaintainerDigest != documentMaintainerDigest(owners) {
		return 0, errDocumentBaselineChanged
	}
	diff, e := apidoc.Compare([]byte(previous), raw)
	if e != nil {
		return 0, e
	}
	if snap.ChangeContext != nil {
		if _, e = documentFieldAdditions([]byte(previous), raw, snap.ChangeContext); e != nil {
			return 0, e
		}
	}
	comparison, _ := json.Marshal(struct {
		Summary   apidoc.ChangeSummary `json:"summary"`
		Truncated bool                 `json:"truncated"`
	}{diff.Summary, diff.Truncated})
	result, e := tx.Exec(`INSERT INTO api_document_versions(application_id,environment,service,run_id,artifact_id,artifact_digest,commit_ref,provider,provider_version,digest,document,created_at,baseline_id,comparison) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, sv.ApplicationID, snap.Environment, sv.Name, snap.RunID, snap.ArtifactID, snap.ArtifactDigest, snap.Commit, snap.Provider, snap.ProviderVersion, digest, string(raw), time.Now().Unix(), baseline, string(comparison))
	if e != nil {
		return 0, e
	}
	id, e = result.LastInsertId()
	if e != nil {
		return 0, e
	}
	if _, e = tx.Exec(`INSERT INTO api_document_protocols(snapshot_id,protocol) VALUES(?,?)`, id, protocol); e != nil {
		return 0, e
	}
	ownerRaw, _ := json.Marshal(owners)
	if _, e = tx.Exec(`INSERT INTO api_document_maintainer_maps(snapshot_id,document) VALUES(?,?)`, id, string(ownerRaw)); e != nil {
		return 0, e
	}
	// Snapshot metadata is inserted only for a new snapshot. Duplicate paths above
	// never overwrite its original owner, source, raw document or comparison.
	if snap.DefaultMaintainer != "" {
		if _, e = tx.Exec(`INSERT INTO api_document_maintainers(snapshot_id,maintainer) VALUES(?,?)`, id, snap.DefaultMaintainer); e != nil {
			return 0, e
		}
	}
	if snap.ChangeContext != nil {
		contextRaw, _ := json.Marshal(snap.ChangeContext)
		if _, e = tx.Exec(`INSERT INTO api_document_change_contexts(snapshot_id,document) VALUES(?,?)`, id, string(contextRaw)); e != nil {
			return 0, e
		}
	}
	if e = recordDocumentLineage(tx, id, baseline, []byte(previous), raw, snap.ChangeContext); e != nil {
		return 0, e
	}
	if e = recordOperationUpdates(tx, id, baseline, newOperations, diff); e != nil {
		return 0, e
	}
	if e = finishDocumentReport(tx, snap.ReportID, id, baseline, "saved", diff.Summary, snap.ChangeContext); e != nil {
		return 0, e
	}
	if e = tx.Commit(); e != nil {
		return 0, errDocumentCommitUnknown
	}
	return id, nil
}
func registerEnvironmentDocumentRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, service func(http.ResponseWriter, *http.Request) *Service, admin func(http.ResponseWriter, *http.Request) bool) {
	registerDocumentReportingRoutes(mux, st, wj, fail, service, admin)
	registerDocumentSearchRoute(mux, st, wj, fail)
	registerDocumentReportRoutes(mux, st, wj, fail)
	registerDocumentOperationHistory(mux, st, wj, fail)
	registerDocumentEditingRoutes(mux, st, wj, fail, actor, service, admin)
	registerDocumentTraceRoutes(mux, st, wj, fail, service)
	environment := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		env := r.URL.Query().Get("environment")
		if env != "" && st.GetEnvironment(env) == nil {
			fail(w, 400, "文档环境不存在")
			return "", false
		}
		return env, true
	}
	mux.HandleFunc("GET /api/api-documents/source", func(w http.ResponseWriter, r *http.Request) {
		sv := service(w, r)
		if sv == nil {
			return
		}
		env, ok := environment(w, r)
		if !ok {
			return
		}
		v, e := st.apiDocumentSource(sv)
		inherited := false
		if env != "" {
			v, inherited, e = st.environmentDocumentSource(sv, env)
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		if r.Header.Get("X-User") != "" && r.Header.Get("X-Role") != "admin" {
			v.Settings = nil
		}
		wj(w, struct {
			apidoc.Source
			Environment    string `json:"environment"`
			Inherited      bool   `json:"inherited"`
			DefaultOnBuild bool   `json:"default_on_build"`
			BuildTool      string `json:"build_tool"`
		}{v, env, inherited, st.documentEnvironmentDefault(env), sv.BuildTool})
	})
	mux.HandleFunc("PUT /api/api-documents/source", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		sv := service(w, r)
		if sv == nil {
			return
		}
		env, ok := environment(w, r)
		if !ok {
			return
		}
		var v apidoc.Source
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&v) != nil {
			fail(w, 400, "文档配置无效")
			return
		}
		if v.Provider != "" {
			if _, e := prepareDocumentPlan(r.Context(), sv, v); e != nil {
				fail(w, 409, e)
				return
			}
		} else {
			v.Settings = json.RawMessage(`{}`)
			v.OnBuild = false
		}
		var e error
		if env == "" {
			e = st.saveAPIDocumentSource(sv, v, actor(r))
		} else {
			e = st.saveEnvironmentDocumentSource(sv, env, v, actor(r))
		}
		if e != nil {
			fail(w, 409, e)
			return
		}
		st.Audit(actor(r), "configure", "api-document-source", sv.ApplicationID, map[string]any{"environment": env, "provider": v.Provider, "on_build": v.OnBuild, "required": v.Required})
		v, _ = st.resolveDocumentSource(sv, env)
		wj(w, v)
	})
	mux.HandleFunc("POST /api/api-documents/import", st.documentReportHTTP(func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		sv := service(w, r)
		if sv == nil {
			return
		}
		env, ok := environment(w, r)
		if !ok {
			return
		}
		raw, context, e := readDocumentSubmission(w, r)
		if e != nil {
			fail(w, 400, "文档超过 8 MiB 或无法读取")
			return
		}
		context, e = st.resolveDocumentChanges(context, raw)
		if e != nil {
			fail(w, 400, e)
			return
		}
		var expected []int64
		if r.URL.Query().Has("expected_baseline_id") {
			id, err := strconv.ParseInt(r.URL.Query().Get("expected_baseline_id"), 10, 64)
			if err != nil || id < 0 {
				fail(w, 400, "文档基线编号无效")
				return
			}
			expected = []int64{id}
		}
		id, e := st.recordAPIDocument(sv, APIDocumentSnapshot{ReportID: r.URL.Query().Get("report_id"), ReportActor: actor(r), Environment: env, Provider: "openapi-import", ExpectedMaintainerDigest: r.URL.Query().Get("expected_maintainer_digest"), ChangeContext: context, DefaultMaintainer: documentKeyMaintainer(r)}, raw, expected...)
		if e != nil {
			status := 400
			if e == errDocumentBaselineChanged {
				status = 409
			}
			fail(w, status, e)
			return
		}
		st.Audit(actor(r), "import", "api-document", fmt.Sprint(id), map[string]any{"application_id": sv.ApplicationID, "environment": env})
		if len(expected) > 0 {
			wj(w, map[string]any{"protocol": func() string { d, _, _, _ := apidoc.Parse(raw); return d.Protocol() }(), "id": id, "duplicate": id <= expected[0], "environment": env})
		} else {
			wj(w, map[string]any{"id": id, "protocol": func() string { d, _, _, _ := apidoc.Parse(raw); return d.Protocol() }()})
		}
	}, false))
	mux.HandleFunc("GET /api/api-documents", func(w http.ResponseWriter, r *http.Request) {
		sv := service(w, r)
		if sv == nil {
			return
		}
		env, ok := environment(w, r)
		if !ok {
			return
		}
		q := `SELECT ` + documentMetadataColumns + ` FROM api_document_versions WHERE application_id=?`
		protocol := r.URL.Query().Get("protocol")
		if protocol == "" {
			protocol = "http"
		}
		if protocol != "http" && protocol != "dubbo" {
			fail(w, 400, "文档协议无效")
			return
		}
		q += ` AND ` + documentProtocolSQL + `=?`
		args := []any{sv.ApplicationID, protocol}
		if r.URL.Query().Has("environment") {
			q += ` AND environment=?`
			args = append(args, env)
		}
		for _, filter := range []string{"artifact_id", "run_id"} {
			if value := r.URL.Query().Get(filter); value != "" {
				id, e := strconv.ParseInt(value, 10, 64)
				if e != nil || id < 1 {
					fail(w, 400, "构建或制品编号无效")
					return
				}
				q += ` AND ` + filter + `=?`
				args = append(args, id)
			}
		}
		q += ` ORDER BY id DESC LIMIT 100`
		rows, e := st.db.Query(q, args...)
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		list := []APIDocumentSnapshot{}
		for rows.Next() {
			v, e := scanDocument(rows)
			if e != nil {
				fail(w, 500, e)
				return
			}
			v.Document = nil
			list = append(list, v)
		}
		if e := rows.Err(); e != nil {
			fail(w, 500, e)
			return
		}
		wj(w, list)
	})
	find := func(w http.ResponseWriter, r *http.Request) (APIDocumentSnapshot, bool) {
		sv := service(w, r)
		if sv == nil {
			return APIDocumentSnapshot{}, false
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v, e := st.apiDocumentSnapshot(sv.ApplicationID, id)
		if e != nil {
			fail(w, 404, "文档快照不存在或不属于当前应用")
			return v, false
		}
		if r.URL.Query().Has("environment") && r.URL.Query().Get("environment") != v.Environment {
			fail(w, 404, "文档快照不属于当前环境")
			return v, false
		}
		return v, true
	}
	mux.HandleFunc("GET /api/api-documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := find(w, r)
		if !ok {
			return
		}
		d, ops, _, e := apidoc.Parse(v.Document)
		if e != nil {
			fail(w, 500, e)
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
		sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
		added, changeContext, err := st.snapshotFieldAdditions(v)
		fieldWarning := ""
		if err != nil {
			fieldWarning = err.Error()
			added = []DocumentFieldAddition{}
		}
		raw := v.Document
		v.Document = nil
		lineage, lineageErr := loadDocumentLineage(st.db, v.ID)
		lineageWarning := lineage.AnalysisError
		if lineageErr != nil {
			lineageWarning = lineageErr.Error()
		}
		wj(w, map[string]any{"update_info": st.documentUpdateInfo(v), "id": v.ID, "info": d.Info, "operations": ops, "document": raw, "digest": v.Digest, "environment": v.Environment, "baseline_id": v.BaselineID, "snapshot": v, "field_additions": added, "change_context": changeContext, "field_additions_error": fieldWarning, "lineage": lineage, "lineage_error": lineageWarning})
	})
	mux.HandleFunc("GET /api/api-documents/{id}/comparison", func(w http.ResponseWriter, r *http.Request) {
		v, ok := find(w, r)
		if !ok {
			return
		}
		baseID := v.BaselineID
		if value := r.URL.Query().Get("base_id"); value != "" {
			var e error
			baseID, e = strconv.ParseInt(value, 10, 64)
			if e != nil || baseID < 0 {
				fail(w, 400, "请选择有效对比基线")
				return
			}
		}
		var base APIDocumentSnapshot
		if baseID > 0 {
			var e error
			base, e = st.apiDocumentSnapshot(v.ApplicationID, baseID)
			if e != nil || base.Environment != v.Environment || base.Protocol != v.Protocol || baseID >= v.ID {
				fail(w, 409, "对比基线必须属于同应用、同环境，且早于当前快照")
				return
			}
		}
		diff, e := apidoc.Compare(base.Document, v.Document)
		if e != nil {
			fail(w, 400, e)
			return
		}
		v.Document = nil
		base.Document = nil
		wj(w, map[string]any{"snapshot": v, "baseline": base, "summary": diff.Summary, "changes": diff.Changes, "truncated": diff.Truncated})
	})
	mux.HandleFunc("GET /api/pipeline/runs/{id}/documentation", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		run := st.GetRun(id)
		if run == nil {
			fail(w, 404, "构建不存在")
			return
		}
		env, frozen, e := st.buildDocumentContext(id)
		if e != nil {
			wj(w, map[string]any{"enabled": false, "environment": "", "snapshots": []APIDocumentSnapshot{}})
			return
		}
		sv := st.GetService(run.Service)
		if sv == nil {
			fail(w, 404, "应用不存在")
			return
		}
		rows, e := st.db.Query(`SELECT `+documentMetadataColumns+` FROM api_document_versions WHERE application_id=? AND run_id=? AND environment=? ORDER BY id DESC`, sv.ApplicationID, id, env)
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		list := []APIDocumentSnapshot{}
		for rows.Next() {
			v, e := scanDocument(rows)
			if e != nil {
				fail(w, 500, e)
				return
			}
			v.Document = nil
			list = append(list, v)
		}
		provider := ""
		if frozen != nil {
			provider = frozen.Source.Provider
		}
		wj(w, map[string]any{"enabled": frozen != nil, "environment": env, "provider": provider, "snapshots": list})
	})
}
