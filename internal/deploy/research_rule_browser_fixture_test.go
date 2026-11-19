package deploy

import (
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

// Explicitly opt-in local fixture. All source Bugs and database records are temporary.
func TestReleaseRulesBrowserFixture(t *testing.T) {
	if os.Getenv("DEVOPS_RULES_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in disposable browser fixture")
	}
	s, state := researchLiveFixture(t, 4)
	state.mu.Lock()
	for i, row := range state.rows {
		row["project"] = "75"
		row["resolution"] = "fixed2"
		if i == 1 {
			row["project"] = "165"
		}
	}
	state.mu.Unlock()
	savePolicyRules(t, s, ResearchReleaseRules{Enabled: true, Default: "unknown", Projects: []ResearchProjectRule{{ProjectID: "75", Name: "原项目规则", Decision: "independent"}}})
	mux := http.NewServeMux()
	registerResearchDeliveryPolicy(mux, s)
	mux.HandleFunc("GET /api/research/overview", func(w http.ResponseWriter, r *http.Request) {
		deliveryJSON(w, map[string]any{"connections": []map[string]any{{"name": "issues", "public_read_configured": true}}})
	})
	mux.HandleFunc("GET /api/research/connections/{name}/account-hint", func(w http.ResponseWriter, r *http.Request) { deliveryJSON(w, map[string]string{"account": ""}) })
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { deliveryJSON(w, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /ui/research", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("assets/app/research.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(b)
	})
	assets, _ := fs.Sub(uiFS, "assets/app/assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	srv := &http.Server{Addr: "127.0.0.1:19981", Handler: mux}
	go srv.ListenAndServe()
	defer srv.Close()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)
	<-sig
}
