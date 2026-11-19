// Minimal independent plugin. Standard library only; no platform imports.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const id = "example-echo"
const version = "1.0.0"
const capability = "example.echo.v1"

func main() {
	address, token := os.Getenv("PLUGIN_LISTEN_ADDR"), os.Getenv("PLUGIN_AUTH_TOKEN")
	if address == "" || token == "" {
		os.Exit(1)
	}
	var config struct {
		Prefix string `json:"prefix"`
	}
	raw, err := os.ReadFile(os.Getenv("PLUGIN_CONFIG_PATH"))
	if err != nil || json.Unmarshal(raw, &config) != nil {
		os.Exit(1)
	}
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { json.NewEncoder(w).Encode(v) }
		fail := func(code, message string) {
			reply(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": false}})
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/describe":
			reply(map[string]any{"protocol": 1, "id": id, "version": version, "capabilities": []string{capability}})
		case r.Method == "GET" && r.URL.Path == "/v1/health":
			reply(map[string]bool{"ok": true})
		case r.Method == "POST" && r.URL.Path == "/v1/test":
			reply(map[string]any{"data": map[string]bool{"ok": true}})
		case r.Method == "POST" && r.URL.Path == "/v1/invoke":
			var req struct {
				Capability string `json:"capability"`
				Operation  string `json:"operation"`
				Actor      struct {
					User string `json:"user"`
					Role string `json:"role"`
				} `json:"actor"`
				Input struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
				fail("invalid_argument", "Invalid JSON")
				return
			}
			if req.Actor.Role != "admin" {
				fail("permission_denied", "Administrator required")
				return
			}
			if req.Capability != capability || req.Operation != "echo" {
				fail("unsupported", "Unsupported capability or operation")
				return
			}
			if len(req.Input.Text) > 1000 {
				fail("invalid_argument", "Text exceeds 1000 bytes")
				return
			}
			if r.Context().Err() != nil {
				return
			}
			reply(map[string]any{"data": map[string]string{"text": config.Prefix + req.Input.Text}})
		default:
			http.NotFound(w, r)
		}
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}
