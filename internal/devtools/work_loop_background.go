package devtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Internal management protocol: the public gateway denies /internal/ paths.
// No bearer material is retained. Each tick supplies a fresh platform directory
// and reads the original Key's CURRENT scopes/revocation from TokenStore.
func (s *ToolService) registerWorkLoopBackground(mux *http.ServeMux) {
	for _, serviceTask := range []bool{false, true} {
		endpoint, base := "/internal/work-loop-advance", "/api/work-loops"
		if serviceTask {
			endpoint, base = "/internal/service-deployment-advance", "/api/service-deployments"
		}
		mux.HandleFunc("POST "+endpoint, func(w http.ResponseWriter, r *http.Request) {
			fail := func(e error) { w.WriteHeader(403); _ = json.NewEncoder(w).Encode(map[string]any{"error": e.Error()}) }
			var req struct {
				TaskID string `json:"task_id"`
				Owner  string `json:"owner"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || len(req.TaskID) != 32 || req.Owner == "" {
				fail(fmt.Errorf("任务身份无效"))
				return
			}
			var users []struct {
				Username string `json:"username"`
				Role     string `json:"role"`
				Enabled  bool   `json:"enabled"`
			}
			if json.Unmarshal([]byte(r.Header.Get("X-MCP-Users")), &users) != nil {
				fail(fmt.Errorf("缺少当前平台身份目录"))
				return
			}
			role := ""
			enabled := []string{}
			for _, u := range users {
				if u.Enabled {
					enabled = append(enabled, u.Username)
					if u.Username == req.Owner {
						role = u.Role
					}
				}
			}
			if role == "" {
				fail(fmt.Errorf("任务授权账号已停用"))
				return
			}
			directory, _ := json.Marshal(enabled)
			reader := &AccessToken{Owner: req.Owner, role: role, verified: true, reviewers: string(directory), identities: r.Header.Get("X-Research-Identities")}
			value, e := s.platformRequest(r.Context(), reader, "GET", base+"/"+req.TaskID, nil, nil)
			if e != nil {
				fail(e)
				return
			}
			task := mapValue(value)
			spec := mapValue(mapValue(task["grant"])["spec"])
			if stringArg(task, "owner") != req.Owner {
				fail(fmt.Errorf("授权人不一致"))
				return
			}
			state := stringArg(task, "state")
			if task["paused"] == true || task["revoked"] == true || (state != "running" && state != "awaiting_acceptance") {
				w.WriteHeader(204)
				return
			}
			deny := func(e error) {
				_, _ = s.platformRequest(r.Context(), reader, "POST", "/internal/"+strings.TrimPrefix(base, "/api/")+"/"+req.TaskID+"/blocked", nil, map[string]any{"revision": task["revision"], "reason": e.Error()})
				fail(e)
			}
			token := reader
			switch stringArg(task, "authority") {
			case "page":
				token.Scopes = append([]string{}, workLoopScopes...)
				token.Applications = []string{"*"}
				token.Environments = []string{stringArg(spec, "environment")}
			case "mcp":
				found := false
				for _, saved := range s.actions.List() {
					if saved.ID == stringArg(task, "authority_key_id") && saved.Owner == req.Owner && !saved.Revoked && saved.DeletedAt == 0 && (saved.ExpiresAt == 0 || saved.ExpiresAt > time.Now().Unix()) {
						copy := saved
						token = &copy
						found = true
						break
					}
				}
				if !found {
					deny(fmt.Errorf("原授权 Key 已过期、撤销或删除；请在任务中续授权"))
					return
				}
				token.role, token.verified, token.reviewers, token.identities = reader.role, true, reader.reviewers, reader.identities
			default:
				deny(fmt.Errorf("旧任务尚未授权后台推进，请核对并续授权"))
				return
			}
			if serviceTask {
				e = serviceDeploymentAccess(token, spec, true)
			} else {
				e = workLoopAccess(token, spec, true)
			}
			if e != nil {
				deny(e)
				return
			}
			result, e := s.platformRequest(r.Context(), token, "POST", base+"/"+req.TaskID+"/advance", nil, map[string]any{"revision": task["revision"]})
			if e != nil {
				fail(e)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		})
	}
}
