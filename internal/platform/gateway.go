package platform

import (
	"embed"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed assets
var shellFS embed.FS

// Gateway 网关：/api/plugins/{name}/* → 插件管理面（127.0.0.1），带鉴权与身份注入。
// 反向代理原生支持 WebSocket（logs 的 /api/tail）。
type Gateway struct {
	mu        sync.RWMutex
	proxies   map[string]*httputil.ReverseProxy
	manifests []*Manifest
	auth      string // 管理密码；空 = 不启用登录（内网）
}

func NewGateway(auth string) *Gateway {
	return &Gateway{proxies: map[string]*httputil.ReverseProxy{}, auth: auth}
}

// SetPlugins 热更新插件表
func (g *Gateway) SetPlugins(ms []*Manifest) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manifests = ms
	for _, m := range ms {
		u, _ := url.Parse("http://" + m.AdminAddr)
		g.proxies[m.Name] = httputil.NewSingleHostReverseProxy(u)
	}
}

// Routes 组装平台壳 + 网关 + 内核 API
func (g *Gateway) Routes(registry *Registry, bus *Bus) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "service": "devopsplatform", "catalog_database": registry.Driver()})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		if g.auth == "" {
			writeJSON(w, map[string]any{"username": "local", "display_name": "本地管理员", "role": RoleAdmin, "auth_enabled": false})
			return
		}
		identity, ok := currentIdentity(r)
		if !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"username": identity.Username, "display_name": identity.DisplayName, "role": identity.Role, "auth_enabled": true})
	})

	// 插件转发
	mux.HandleFunc("/api/plugins/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.RLock()
		defer g.mu.RUnlock()
		// path: /api/plugins/{name}/rest...
		rest := r.URL.Path[len("/api/plugins/"):]
		name := rest
		if i := indexByte(rest, '/'); i >= 0 {
			name = rest[:i]
			r.URL.Path = rest[i:]
		} else {
			r.URL.Path = "/"
		}
		p, ok := g.proxies[name]
		if !ok {
			http.Error(w, `{"error":"plugin not found"}`, 404)
			return
		}
		for _, m := range g.manifests {
			if m.Name != name {
				continue
			}
			if m.Internal {
				http.NotFound(w, r)
				return
			}
			if m.Backend != "" {
				if (name == "dbms" || name == "redis") && r.URL.Path != "/ui" && !strings.HasPrefix(r.URL.Path, "/assets/") && !strings.HasPrefix(r.URL.Path, "/api/workbench/"+name+"/") {
					http.NotFound(w, r)
					return
				}
				if r.URL.Path == "/ui" {
					r.URL.Path = m.UIPath
				}
			}
			break
		}
		user, role := currentUser(r), currentRole(r)
		if g.auth == "" {
			user, role = "local", RoleAdmin
		}
		r.Header.Set("X-User", user) // 身份注入
		r.Header.Set("X-Role", role)
		p.ServeHTTP(w, r)
	})

	// 资源中心属于平台公共能力。当前资源执行服务与部署执行器运行在同一
	// 进程，但公共页面只使用这个稳定入口，不感知部署插件的内部路由。
	mux.HandleFunc("/api/resources/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.RLock()
		p, ok := g.proxies["delivery-core"]
		if !ok {
			p, ok = g.proxies["deploy"]
		}
		g.mu.RUnlock()
		if !ok {
			http.Error(w, `{"error":"资源服务不可用"}`, http.StatusServiceUnavailable)
			return
		}
		r.URL.Path = "/api/" + r.URL.Path[len("/api/resources/"):]
		user, role := currentUser(r), currentRole(r)
		if g.auth == "" {
			user, role = "local", RoleAdmin
		}
		r.Header.Set("X-User", user)
		r.Header.Set("X-Role", role)
		p.ServeHTTP(w, r)
	})

	// 内核 API
	mux.HandleFunc("GET /api/plugins", func(w http.ResponseWriter, r *http.Request) {
		g.mu.RLock()
		defer g.mu.RUnlock()
		visible := []*Manifest{}
		for _, m := range g.manifests {
			if !m.Internal {
				visible = append(visible, m)
			}
		}
		writeJSON(w, visible)
	})
	registry.HandleRoutes(mux)
	bus.HandleRoutes(mux)

	// 用户管理保持刻意简单：只有管理员和成员两个固定角色。
	g.registerUserSync(mux, registry)
	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		users, err := registry.Users()
		if err != nil {
			http.Error(w, `{"error":"读取用户失败"}`, http.StatusInternalServerError)
			return
		}
		if len(users) == 0 && g.auth == "" {
			users = []User{{Username: "local", DisplayName: "本地管理员", Role: RoleAdmin, Enabled: true}}
		}
		writeJSON(w, users)
	})
	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username    string `json:"username"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
			Password    string `json:"password"`
			Enabled     *bool  `json:"enabled"`
		}
		if err := readJSONBodyError(r, &body); err != nil {
			http.Error(w, `{"error":"用户参数格式错误"}`, http.StatusBadRequest)
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		user, err := registry.SaveUser(User{Username: body.Username, DisplayName: body.DisplayName, Role: body.Role, Enabled: enabled}, body.Password)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, user)
	})
	mux.HandleFunc("PUT /api/users/{username}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
			Password    string `json:"password"`
			Enabled     bool   `json:"enabled"`
		}
		if err := readJSONBodyError(r, &body); err != nil {
			http.Error(w, `{"error":"用户参数格式错误"}`, http.StatusBadRequest)
			return
		}
		username := r.PathValue("username")
		if username == currentUser(r) && (!body.Enabled || body.Role != RoleAdmin) {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "不能停用当前账号或移除自己的管理员角色"})
			return
		}
		user, err := registry.SaveUser(User{Username: username, DisplayName: body.DisplayName, Role: body.Role, Enabled: body.Enabled}, body.Password)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if username != currentUser(r) {
			revokeUserSessions(username)
		}
		writeJSON(w, user)
	})
	mux.HandleFunc("DELETE /api/users/{username}", func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if err := registry.DeleteUser(username, currentUser(r)); err != nil {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		revokeUserSessions(username)
		writeJSON(w, map[string]bool{"ok": true})
	})

	// 登录（启用 -auth 时）
	if g.auth != "" {
		mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if readJSONBodyError(r, &body) != nil {
				http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
				return
			}
			user, err := registry.Authenticate(body.Username, body.Password)
			if err != nil {
				http.Error(w, `{"error":"用户名或密码错误"}`, http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "lp_session", Value: sessionToken(*user), Path: "/", HttpOnly: true,
				Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 24 * 60 * 60, Expires: time.Now().Add(24 * time.Hour)})
			writeJSON(w, map[string]any{"ok": true, "username": user.Username, "role": user.Role})
		})
		mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
			clearSession(r)
			http.SetCookie(w, &http.Cookie{Name: "lp_session", Value: "", Path: "/", HttpOnly: true, MaxAge: -1, Expires: time.Unix(0, 0)})
			writeJSON(w, map[string]bool{"ok": true})
		})
	}

	// 壳
	shell, _ := fs.Sub(shellFS, "assets/assets")
	mux.Handle("GET /static/", http.FileServer(http.FS(shell)))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(shell))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			b, _ := shellFS.ReadFile("assets/shell.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store") // 壳与插件 UI 开发期禁缓存，改版即生效
			w.Write(b)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(404)
		w.Write([]byte(`<html><body style="background:#0d1117;color:#8b949e;font:14px -apple-system,sans-serif;padding:40px">404 — 路径不存在: ` + r.URL.Path + `</body></html>`))
	})

	return g.authMiddleware(mux)
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
