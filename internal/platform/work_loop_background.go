package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// RunWorkLoops is lifecycle-owned by the platform process. It never stores
// session credentials. Original execution CAS/intent guards serialize concurrent
// page/MCP/background advances and prevent replay after a lost response.
func (g *Gateway) RunWorkLoops(ctx context.Context, registry *Registry) {
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.workLoopTick(ctx, registry)
		}
	}
}
func (g *Gateway) workLoopTick(ctx context.Context, registry *Registry) {
	if g.auth == "" {
		return
	}
	g.mu.RLock()
	deploy, devtools := "", ""
	for _, m := range g.manifests {
		if m.Name == "delivery-core" || m.Name == "deploy" && deploy == "" {
			deploy = "http://" + m.AdminAddr
		}
		if m.Name == "devtools" {
			devtools = "http://" + m.AdminAddr
		}
	}
	g.mu.RUnlock()
	if deploy == "" || devtools == "" {
		return
	}
	users, e := registry.Users()
	if e != nil {
		return
	}
	client := &http.Client{Timeout: 100 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, user := range users {
		if !user.Enabled || user.Username == "local" {
			continue
		}
		for _, taskRoute := range []struct{ base, advance string }{{"/api/work-loops", "/internal/work-loop-advance"}, {"/api/service-deployments", "/internal/service-deployment-advance"}, {"/api/delivery/publications", "/internal/delivery-publication-advance"}} {
			req, e := http.NewRequestWithContext(ctx, "GET", deploy+taskRoute.base, nil)
			if e != nil {
				return
			}
			req.Header.Set("X-User", user.Username)
			resp, e := client.Do(req)
			if e != nil {
				continue
			}
			var tasks []struct {
				ID      string `json:"id"`
				State   string `json:"state"`
				Paused  bool   `json:"paused"`
				Revoked bool   `json:"revoked"`
			}
			e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&tasks)
			resp.Body.Close()
			if e != nil || resp.StatusCode != 200 {
				continue
			}
			for _, task := range tasks {
				if task.Paused || task.Revoked || (task.State != "running" && task.State != "awaiting_acceptance") {
					continue
				}
				// Refresh immediately before each dispatch, not only at process startup.
				current, e := registry.Users()
				if e != nil {
					return
				}
				identities, e := registry.UserIdentities()
				if e != nil {
					return
				}
				entries := []map[string]any{}
				for _, u := range current {
					entries = append(entries, map[string]any{"username": u.Username, "role": u.Role, "enabled": u.Enabled})
				}
				directory, _ := json.Marshal(entries)
				links, _ := json.Marshal(identities)
				if len(directory) > 60000 || len(links) > 60000 {
					return
				}
				body, _ := json.Marshal(map[string]any{"task_id": task.ID, "owner": user.Username})
				r, e := http.NewRequestWithContext(ctx, "POST", devtools+taskRoute.advance, bytes.NewReader(body))
				if e != nil {
					return
				}
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-MCP-Users", string(directory))
				r.Header.Set("X-Research-Identities", string(links))
				result, e := client.Do(r)
				if e != nil {
					continue
				}
				_, _ = io.Copy(io.Discard, io.LimitReader(result.Body, 8<<20))
				result.Body.Close()
			}
		}
	}
}
