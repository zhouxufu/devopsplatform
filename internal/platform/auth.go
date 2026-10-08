package platform

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 轻量登录：管理员/成员两个内置角色 + 24h 会话 cookie。

type sessionIdentity struct {
	Username    string
	DisplayName string
	Role        string
	Expires     time.Time
}

var (
	sessionsMu sync.Mutex
	sessions   = map[string]sessionIdentity{}
)

func sessionToken(user User) string {
	b := make([]byte, 16)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	sessionsMu.Lock()
	for key, session := range sessions {
		if time.Now().After(session.Expires) {
			delete(sessions, key)
		}
	}
	sessions[tok] = sessionIdentity{Username: user.Username, DisplayName: user.DisplayName, Role: user.Role, Expires: time.Now().Add(24 * time.Hour)}
	sessionsMu.Unlock()
	return tok
}

func currentIdentity(r *http.Request) (sessionIdentity, bool) {
	c, err := r.Cookie("lp_session")
	if err != nil {
		return sessionIdentity{}, false
	}
	sessionsMu.Lock()
	session, ok := sessions[c.Value]
	if ok && time.Now().After(session.Expires) {
		delete(sessions, c.Value)
		ok = false
	}
	sessionsMu.Unlock()
	if ok && time.Now().Before(session.Expires) {
		return session, true
	}
	return sessionIdentity{}, false
}

func currentUser(r *http.Request) string {
	identity, _ := currentIdentity(r)
	return identity.Username
}

func currentRole(r *http.Request) string {
	identity, _ := currentIdentity(r)
	return identity.Role
}

func revokeUserSessions(username string) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	for token, session := range sessions {
		if session.Username == username {
			delete(sessions, token)
		}
	}
}

func clearSession(r *http.Request) {
	cookie, err := r.Cookie("lp_session")
	if err != nil {
		return
	}
	sessionsMu.Lock()
	delete(sessions, cookie.Value)
	sessionsMu.Unlock()
}

func readJSONBody(r *http.Request, v any) {
	_ = readJSONBodyError(r, v)
}

func readJSONBodyError(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	return dec.Decode(v)
}

// authMiddleware 启用密码时：登录接口与静态资源放行，其余需有效会话
func (g *Gateway) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, `{"error":"cross-origin request rejected"}`, http.StatusForbidden)
					return
				}
			}
		}
		if g.auth == "" {
			next.ServeHTTP(w, r)
			return
		}
		// MCP 使用独立、细粒度的 Bearer Token，由 devtools 插件校验。
		// 这里只放行精确端点；令牌管理和审计 API 仍要求控制台会话。
		if r.URL.Path == "/api/plugins/devtools/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		// Deploy webhooks carry an application-scoped random token and are called
		// by Git providers that do not have a browser session.
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/plugins/deploy/api/hooks/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}
		if identity, ok := currentIdentity(r); ok {
			if requiresAdmin(r) && identity.Role != RoleAdmin {
				http.Error(w, `{"error":"该操作仅管理员可执行"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// 登录界面由壳前端渲染，因此首页依赖的 JS/CSS 必须允许匿名读取。
		// 这里只放行 GET/HEAD 静态资源，不放行任何插件或业务 API。
		if r.URL.Path == "/" || ((r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			(strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasPrefix(r.URL.Path, "/static/"))) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, `{"error":"unauthorized"}`, 401)
	})
}

func requiresAdmin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return false
	}
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/users") || strings.HasPrefix(p, "/api/resources/") || strings.HasPrefix(p, "/api/registry/applications") {
		return true
	}
	if !strings.HasPrefix(p, "/api/plugins/deploy/api/") {
		return false
	}
	rest := strings.TrimPrefix(p, "/api/plugins/deploy/api/")
	if rest == "services" || rest == "setup/environments" || rest == "environments" || strings.HasPrefix(rest, "environments/") {
		return true
	}
	if r.Method == http.MethodDelete && strings.HasPrefix(rest, "services/") {
		return true
	}
	if r.Method == http.MethodPut && strings.HasPrefix(rest, "services/") {
		return true
	}
	for _, prefix := range []string{"connections", "hosts", "host-groups", "variable-groups", "runners"} {
		if rest == prefix || strings.HasPrefix(rest, prefix+"/") {
			return true
		}
	}
	return false
}

func securePasswordEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
