package deploy

import (
	"encoding/json"
	"fmt"
	"logplatform/pkg/apidoc"
	"net/http"
	"strconv"
)

func registerDocumentEditingRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string, service func(http.ResponseWriter, *http.Request) *Service, admin func(http.ResponseWriter, *http.Request) bool) {
	for _, preview := range []bool{true, false} {
		preview := preview
		path := "POST /api/api-documents/{id}/edit"
		if preview {
			path += "-preview"
		}
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if !admin(w, r) {
				return
			}
			sv := service(w, r)
			if sv == nil {
				return
			}
			id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
			base, err := st.apiDocumentSnapshot(sv.ApplicationID, id)
			if err != nil {
				fail(w, 404, "文档快照不存在或不属于当前应用")
				return
			}
			env := r.URL.Query().Get("environment")
			if env == "" || env != base.Environment || st.GetEnvironment(env) == nil {
				fail(w, 400, "请选择文档原属的具体环境，历史未区分环境文档请另行导入")
				return
			}
			keyMaintainer := documentKeyMaintainer(r)
			raw, changeContext, err := readDocumentSubmission(w, r, base.Document)
			if err != nil {
				fail(w, 400, err)
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
			added, err := documentFieldAdditions(base.Document, raw, changeContext)
			fieldWarning := ""
			if err != nil {
				if changeContext != nil {
					fail(w, 400, err)
					return
				}
				fieldWarning = err.Error()
				added = []DocumentFieldAddition{}
			}
			if preview {
				st.apiDocumentMu.Lock()
				defer st.apiDocumentMu.Unlock()
				_, latest, err := st.documentBaseline(sv, env)
				if err != nil {
					fail(w, 500, err)
					return
				}
				if latest.ID != base.ID {
					fail(w, 409, "文档已有新版本，请保留草稿并重新载入最新版本核对")
					return
				}
				owners, ownerChanges, err := st.previewDocumentMaintainers(ops, base, keyMaintainer)
				if err != nil {
					fail(w, 500, err)
					return
				}
				diff, err := apidoc.Compare(base.Document, raw)
				if err != nil {
					fail(w, 400, err)
					return
				}
				wj(w, map[string]any{"candidate": json.RawMessage(raw), "field_additions": added, "change_context": changeContext, "field_additions_error": fieldWarning, "source_id": base.ID, "maintainers": owners, "maintainer_changes": ownerChanges, "maintainer_digest": documentMaintainerDigest(owners), "service": sv.Name, "environment": env, "digest": digest, "info": doc.Info, "operation_count": len(ops), "summary": diff.Summary, "changes": diff.Changes, "truncated": diff.Truncated, "unchanged": digest == base.Digest})
				return
			}
			expected, err := strconv.ParseInt(r.URL.Query().Get("expected_baseline_id"), 10, 64)
			if err != nil || expected != base.ID || r.URL.Query().Get("expected_digest") != digest {
				fail(w, 409, "请先重新预览本次编辑内容")
				return
			}
			newID, err := st.recordAPIDocument(sv, APIDocumentSnapshot{Environment: env, Provider: "openapi-edit", DefaultMaintainer: keyMaintainer, ChangeContext: changeContext, ExpectedMaintainerDigest: r.URL.Query().Get("expected_maintainer_digest")}, raw, base.ID)
			if err != nil {
				status := 400
				if err == errDocumentBaselineChanged {
					status = 409
				}
				fail(w, status, err)
				return
			}
			if newID != base.ID {
				st.Audit(actor(r), "edit", "api-document", fmt.Sprint(newID), map[string]any{"source_id": base.ID, "application_id": sv.ApplicationID, "environment": env})
			}
			wj(w, map[string]any{"id": newID, "source_id": base.ID, "unchanged": newID == base.ID, "environment": env})
		})
	}
}
