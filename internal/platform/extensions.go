package platform

import (
	"encoding/json"
	"errors"
	"io"
	"logplatform/internal/extensions"

	p "logplatform/pkg/extensionprotocol"
	"logplatform/pkg/workflownode"
	"net/http"
	"strings"
)

func (g *Gateway) extensionRoutes(mux *http.ServeMux) {
	m := g.extensions
	if m == nil {
		return
	}
	actor := func(r *http.Request) p.Actor {
		if g.auth == "" {
			return p.Actor{User: "local", Role: RoleAdmin}
		}
		return p.Actor{User: currentUser(r), Role: currentRole(r)}
	}
	errorJSON := func(w http.ResponseWriter, err error) {
		var e *p.Error
		if !errors.As(err, &e) {
			e = &p.Error{Code: "internal", Message: "插件管理操作失败"}
		}
		status := http.StatusBadRequest
		switch e.Code {
		case "permission_denied":
			status = 403
		case "unavailable":
			status = 503
		case "resource_exhausted":
			status = 429
		case "internal":
			status = 500
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"error": e.Message, "code": e.Code})
	}
	admin := func(w http.ResponseWriter, r *http.Request) bool {
		if actor(r).Role != RoleAdmin {
			errorJSON(w, &p.Error{Code: "permission_denied", Message: "仅管理员可管理能力插件"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/extensions", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, m.List(actor(r).Role == RoleAdmin)) })
	mux.HandleFunc("GET /api/workflow-node-providers", func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		g.mu.RLock()
		names := []string{}
		for _, manifest := range g.manifests {
			names = append(names, manifest.Name)
		}
		g.mu.RUnlock()
		if d := workflownode.SharedDescriptor(workflownode.APITestProvider, strings.Join(names, ",")); d != nil {
			items = append(items, map[string]any{"id": workflownode.APITestProvider, "name": "接口中心", "version": d.ProviderVersion, "state": "running", "runtime": "shared", "nodes": []workflownode.Descriptor{*d}})
		}
		for _, v := range m.List(actor(r).Role == RoleAdmin) {
			supported := false
			for _, cap := range v.Manifest.Capabilities {
				if cap == workflownode.Capability {
					supported = true
				}
			}
			if !supported {
				continue
			}
			item := map[string]any{"id": v.Manifest.ID, "name": v.Manifest.Name, "version": v.Manifest.Version, "state": v.State, "nodes": []workflownode.Descriptor{}}
			if v.State == "running" {
				out, err := m.Invoke(r.Context(), v.Manifest.ID, p.Request{Capability: workflownode.Capability, Operation: "describe", Actor: actor(r)}, false)
				if err == nil && out.Error == nil {
					var d workflownode.Descriptor
					if json.Unmarshal(out.Data, &d) == nil && workflownode.ValidateDescriptor(d) == nil {
						item["nodes"] = []workflownode.Descriptor{d}
					}
				}
			}
			items = append(items, item)
		}
		writeJSON(w, items)
	})
	mux.HandleFunc("POST /api/extensions", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 65<<20)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			errorJSON(w, &p.Error{Code: "invalid_argument", Message: "安装包格式错误或超过 64 MiB"})
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("package")
		if err != nil {
			errorJSON(w, &p.Error{Code: "invalid_argument", Message: "请选择 ZIP 插件包"})
			return
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, 64<<20+1))
		if err != nil {
			errorJSON(w, err)
			return
		}
		v, err := m.Install(raw)
		if err != nil {
			errorJSON(w, err)
			return
		}
		writeJSON(w, v)
	})
	mux.HandleFunc("PUT /api/extensions/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		var config map[string]any
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&config) != nil || config == nil {
			errorJSON(w, &p.Error{Code: "invalid_argument", Message: "配置格式错误"})
			return
		}
		if err := m.Configure(r.PathValue("id"), config); err != nil {
			errorJSON(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	for op := range map[string]bool{"enable": true, "disable": true, "test": true} {
		operation := op
		mux.HandleFunc("POST /api/extensions/{id}/"+operation, func(w http.ResponseWriter, r *http.Request) {
			if !admin(w, r) {
				return
			}
			id := r.PathValue("id")
			var err error
			switch operation {
			case "enable":
				err = m.Enable(id)
			case "disable":
				err = m.Disable(id)
			case "test":
				var result *p.Response
				result, err = m.Invoke(r.Context(), id, p.Request{Actor: actor(r)}, true)
				if err == nil && result.Error != nil {
					err = result.Error
				}
			}
			if err != nil {
				errorJSON(w, err)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		})
	}
	mux.HandleFunc("DELETE /api/extensions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !admin(w, r) {
			return
		}
		if err := m.Remove(r.PathValue("id")); err != nil {
			errorJSON(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/extensions/{id}/invoke", func(w http.ResponseWriter, r *http.Request) {
		var req p.Request
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
			errorJSON(w, &p.Error{Code: "invalid_argument", Message: "调用请求格式错误"})
			return
		}
		req.Actor = actor(r)
		result, err := m.Invoke(r.Context(), r.PathValue("id"), req, false)
		if err != nil {
			errorJSON(w, err)
			return
		}
		writeJSON(w, result)
	})
}
func (g *Gateway) SetExtensions(m *extensions.Manager) { g.extensions = m }
