package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var uiFS embed.FS

// registerUI 内嵌查询页与静态资源（flatpickr 离线可用）
func (a *APIServer) registerUI(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("assets/app/logs.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /ui-legacy", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("assets/ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	sub, _ := fs.Sub(uiFS, "assets/app/assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui", 302)
			return
		}
	})
}
