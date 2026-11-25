package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"logplatform/pkg/apidoc"
	"net/http"
	"strings"
	"unicode/utf8"
)

var errDocumentBaselineChanged = errors.New("接口文档基线已变化，请重新预览后确认")

// Reuse the existing gateway-owned MCP user-directory envelope. The gateway
// strips/replaces X-MCP-Users from external requests; devtools forwards only the
// bound owner after current enabled-user/role validation. Deploy's admin API is
// internal, with the same trusted-hop boundary as X-User and X-Role.
func documentKeyMaintainer(r *http.Request) string {
	owner := r.Header.Get("X-User")
	if owner == "" || owner == "local" || r.Header.Get("X-Role") != "admin" || strings.TrimSpace(owner) != owner || strings.ContainsAny(owner, "\x00\r\n") || utf8.RuneCountInString(owner) > 191 {
		return ""
	}
	raw := r.Header.Get("X-MCP-Users")
	var users []struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		Enabled  bool   `json:"enabled"`
	}
	if len(raw) > 60000 || json.Unmarshal([]byte(raw), &users) != nil || len(users) != 1 || users[0].Username != owner || users[0].Role != "admin" || !users[0].Enabled {
		return ""
	}
	return owner
}

func applyDocumentMaintainer(ops []apidoc.Operation, fallback string) {
	for i := range ops {
		if ops[i].Maintainer == "" {
			ops[i].Maintainer = fallback
		}
	}
}

// All writers use recordAPIDocument's mutex. Preview and commit share the same
// immutable baseline; no MCP-specific document store or comparison engine.
func (s *Store) documentBaseline(sv *Service, env string, protocols ...string) (map[string]any, APIDocumentSnapshot, error) {
	protocol := "http"
	if len(protocols) > 0 && protocols[0] == "dubbo" {
		protocol = "dubbo"
	}
	v, err := scanDocument(s.db.QueryRow(`SELECT `+documentColumns+` FROM api_document_versions WHERE application_id=? AND environment=? AND `+documentProtocolSQL+`=? ORDER BY id DESC LIMIT 1`, sv.ApplicationID, env, protocol))
	if err != nil && err != sql.ErrNoRows {
		return nil, v, err
	}
	return map[string]any{"application_id": sv.ApplicationID, "environment": env, "id": v.ID, "digest": v.Digest}, v, nil
}

func registerDocumentReportingRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), service func(http.ResponseWriter, *http.Request) *Service, admin func(http.ResponseWriter, *http.Request) bool) {
	context := func(w http.ResponseWriter, r *http.Request) (*Service, string) {
		sv := service(w, r)
		if sv == nil {
			return nil, ""
		}
		env := r.URL.Query().Get("environment")
		if env == "" || st.GetEnvironment(env) == nil {
			fail(w, 400, "请选择已有文档环境")
			return nil, ""
		}
		return sv, env
	}
	mux.HandleFunc("GET /api/api-documents/baseline", func(w http.ResponseWriter, r *http.Request) {
		sv, env := context(w, r)
		if sv == nil {
			return
		}
		st.apiDocumentMu.Lock()
		defer st.apiDocumentMu.Unlock()
		protocol := r.URL.Query().Get("protocol")
		if protocol != "" && protocol != "http" && protocol != "dubbo" {
			fail(w, 400, "文档协议无效")
			return
		}
		baseline, _, err := st.documentBaseline(sv, env, protocol)
		if err != nil {
			fail(w, 500, err)
			return
		}
		wj(w, baseline)
	})
	mux.HandleFunc("POST /api/api-documents/import-preview", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		sv, env := context(w, r)
		if sv == nil {
			return
		}
		raw, changeContext, err := readDocumentSubmission(w, r)
		if err != nil {
			fail(w, 400, "文档超过 8 MiB 或无法读取")
			return
		}
		doc, ops, digest, err := apidoc.Parse(raw)
		if err != nil {
			fail(w, 400, err)
			return
		}
		changeContext, err = st.resolveDocumentChanges(changeContext, raw)
		if err != nil {
			fail(w, 400, err)
			return
		}
		st.apiDocumentMu.Lock()
		defer st.apiDocumentMu.Unlock()
		baseline, previous, err := st.documentBaseline(sv, env, doc.Protocol())
		if err != nil {
			fail(w, 500, err)
			return
		}
		var duplicateID int64
		err = st.db.QueryRow(`SELECT id FROM api_document_versions WHERE application_id=? AND environment=? AND digest=? ORDER BY id DESC LIMIT 1`, sv.ApplicationID, env, digest).Scan(&duplicateID)
		if err != nil && err != sql.ErrNoRows {
			fail(w, 500, err)
			return
		}
		defaultMaintainer := documentKeyMaintainer(r)
		if duplicateID > 0 {
			existing, e := st.apiDocumentSnapshot(sv.ApplicationID, duplicateID)
			if e != nil {
				fail(w, 500, e)
				return
			}
			defaultMaintainer = existing.DefaultMaintainer
			changeContext, err = st.documentChangeContext(existing.ID)
			if err != nil {
				fail(w, 500, err)
				return
			}
		}
		added, err := documentFieldAdditions(previous.Document, raw, changeContext)
		if duplicateID > 0 {
			existing, _ := st.apiDocumentSnapshot(sv.ApplicationID, duplicateID)
			added, changeContext, err = st.snapshotFieldAdditions(existing)
		}
		fieldWarning := ""
		if err != nil {
			if changeContext != nil {
				fail(w, 400, err)
				return
			}
			fieldWarning = err.Error()
			added = []DocumentFieldAddition{}
		}
		owners, ownerChanges, err := st.previewDocumentMaintainers(ops, previous, defaultMaintainer)
		if err != nil {
			fail(w, 500, err)
			return
		}
		if duplicateID > 0 {
			owners, err = loadDocumentMaintainers(st.db, duplicateID)
			if err != nil {
				fail(w, 500, err)
				return
			}
			if owners == nil {
				// Duplicates of legacy snapshots use the original contact/default
				// rule, not the priority policy for newly reported operations.
				owners = map[string]DocumentMaintainer{}
				for _, op := range ops {
					source := "legacy-default"
					maintainer := op.Maintainer
					if apidoc.ExplicitMaintainer(op) != "" {
						source = "explicit"
					} else if op.Maintainer != "" {
						source = "document"
					} else {
						maintainer = defaultMaintainer
						if maintainer == "" {
							source = "unspecified"
						}
					}
					owners[op.ID] = DocumentMaintainer{maintainer, source}
				}
			}
			ownerChanges = []DocumentMaintainerChange{}
		}
		if err = applyFrozenDocumentMaintainers(ops, owners, defaultMaintainer); err != nil {
			fail(w, 500, err)
			return
		}
		maintainers := map[string]string{}
		sources := map[string]string{}
		for id, value := range owners {
			sources[id] = value.Source
		}
		for _, op := range ops {
			maintainers[op.ID] = op.Maintainer
		}
		diff, err := apidoc.Compare(previous.Document, raw)
		if err != nil {
			fail(w, 400, err)
			return
		}
		wj(w, map[string]any{"field_additions": added, "change_context": changeContext, "field_additions_error": fieldWarning, "default_maintainer": defaultMaintainer, "maintainer_sources": sources, "maintainer_changes": ownerChanges, "maintainer_digest": documentMaintainerDigest(owners), "maintainers": maintainers, "service": sv.Name, "environment": env, "info": doc.Info, "operation_count": len(ops), "digest": digest, "baseline": baseline, "duplicate": duplicateID > 0, "existing_document_id": duplicateID, "summary": diff.Summary, "changes": diff.Changes, "truncated": diff.Truncated})
	})
}
