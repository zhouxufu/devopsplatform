package rulesplugin

import (
	"crypto/subtle"
	"encoding/json"
	p "logplatform/pkg/extensionprotocol"
	rules "logplatform/pkg/releasepolicy"
	"net/http"
)

const ID = "release-rules"
const Version = "1.0.0"

// This compiler has no credentials, network access or platform database access.
func Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		if r.Method == "GET" && r.URL.Path == "/v1/describe" {
			write(p.Description{Protocol: p.Major, ID: ID, Version: Version, Capabilities: []string{rules.Capability}})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/health" || r.Method == "POST" && r.URL.Path == "/v1/test" {
			write(map[string]bool{"ok": true})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/invoke" {
			http.NotFound(w, r)
			return
		}
		fail := func(code, msg string) { write(p.Response{Error: &p.Error{Code: code, Message: msg}}) }
		var req p.Request
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
			fail("invalid_argument", "规则请求格式无效")
			return
		}
		if req.Actor.Role != "admin" {
			fail("permission_denied", "仅管理员可编译规则")
			return
		}
		if req.Capability != rules.Capability {
			fail("unsupported", "不支持此能力")
			return
		}
		var data any
		if req.Operation == "schema" {
			data = map[string]any{"version": 1, "fields": rules.Fields}
		} else if req.Operation == "compile" {
			var input struct {
				Policy rules.Policy `json:"policy"`
			}
			if json.Unmarshal(req.Input, &input) != nil {
				fail("invalid_argument", "规则格式无效")
				return
			}
			if err := rules.Validate(input.Policy); err != nil {
				fail("invalid_argument", err.Error())
				return
			}
			data = input
		} else {
			fail("unsupported", "不支持此操作")
			return
		}
		raw, _ := json.Marshal(data)
		write(p.Response{Data: raw})
	})
}
