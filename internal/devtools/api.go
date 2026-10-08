package devtools

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

//go:embed assets
var uiFS embed.FS

func Routes(tokens *TokenStore, tools *ToolService) *http.ServeMux {
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.Handle("/mcp", NewMCPHandler(tokens, tools))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "transport": "streamable-http", "tools": toolNames(tools.Tools())})
	})
	mux.HandleFunc("GET /api/tokens", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, tokens.List())
	})
	mux.HandleFunc("POST /api/tokens", func(w http.ResponseWriter, r *http.Request) {
		var req CreateTokenRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		token, raw, err := tokens.Create(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		token.Hash = ""
		writeJSON(w, http.StatusCreated, map[string]any{"token": token, "secret": raw})
	})
	mux.HandleFunc("DELETE /api/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := tokens.Revoke(strings.TrimSpace(r.PathValue("id"))); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/audit", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		writeJSON(w, http.StatusOK, tokens.AuditEvents(limit))
	})
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		body, err := uiFS.ReadFile("assets/ui.html")
		if err != nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	return mux
}
