package platform

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestApplicationArchivePreservesIdentityAndHistory(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, err := r.Observe("archive-fixture", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDeployment(DeploymentState{Service: a.Name, Environment: "test", Version: "original"}); err != nil {
		t.Fatal(err)
	}
	if err := r.archiveApplication(a.ApplicationID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := r.archiveApplication(a.ApplicationID, "admin"); err != nil {
		t.Fatal(err)
	}
	active, _ := r.ListActive()
	all, _ := r.List()
	if len(active) != 0 || len(all) != 1 || all[0].ApplicationID != a.ApplicationID || all[0].Name != a.Name || all[0].Status != "archived" {
		t.Fatal(active, all)
	}
	if history := r.DeploymentStates(); len(history) != 1 || history[0].Version != "original" {
		t.Fatal(history)
	}
	var n int
	r.db.QueryRow(`SELECT COUNT(*) FROM application_lifecycle_events WHERE application_id=? AND actor='admin'`, a.ApplicationID).Scan(&n)
	if n != 1 {
		t.Fatal("audit must be idempotent", n)
	}
	restored, err := r.Observe(a.Name, "logs", "")
	if err != nil || restored.ApplicationID != a.ApplicationID || restored.Status != "archived" {
		t.Fatal(restored, err)
	}
}

func TestArchivedTelemetryStaysHiddenAcrossRestartAndExplicitReadoption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Observe("archived-live", "logs", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.archiveApplication(a.ApplicationID, "admin"); err != nil {
		t.Fatal(err)
	}
	r.Close()
	r, err = OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, source := range []string{"logs", "otel", "logs"} {
		observed, err := r.Observe(a.Name, source, a.ApplicationID)
		if err != nil || observed.ApplicationID != a.ApplicationID || observed.Status != "archived" {
			t.Fatal(observed, err)
		}
		active, err := r.ListActive()
		if err != nil || len(active) != 0 {
			t.Fatal(active, err)
		}
	}
	all, err := r.List()
	if err != nil || len(all) != 1 || all[0].ApplicationID != a.ApplicationID {
		t.Fatal(all, err)
	}
	restored, err := r.Observe(a.Name, "deploy", "")
	if err != nil || restored.ApplicationID != a.ApplicationID || restored.Status != "managed" {
		t.Fatal(restored, err)
	}
	active, err := r.ListActive()
	if err != nil || len(active) != 1 {
		t.Fatal(active, err)
	}
}

func TestApplicationArchiveBlocksCurrentDeploymentAndRollsBackOnAuditFailure(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, _ := r.Observe("linked", "manual", "")
	// A stale client might have observed only the manual source.
	r.Observe(a.Name, "deploy", a.ApplicationID)
	if err := r.archiveApplication(a.ApplicationID, "admin"); !errors.Is(err, errApplicationHasDeployment) {
		t.Fatal(err)
	}
	active, _ := r.ListActive()
	if len(active) != 1 {
		t.Fatal(active)
	}
	r.DetachSource(a.Name, "deploy", a.ApplicationID)
	if _, err := r.db.Exec(`DROP TABLE application_lifecycle_events`); err != nil {
		t.Fatal(err)
	}
	if err := r.archiveApplication(a.ApplicationID, "admin"); err == nil {
		t.Fatal("expected audit failure")
	}
	active, _ = r.ListActive()
	if len(active) != 1 {
		t.Fatal("archive committed without audit", active)
	}
}

func TestApplicationArchiveHTTPAuthorizationAndStatuses(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, _ := r.Observe("manual", "manual", "")
	linked, _ := r.Observe("linked", "deploy", "")
	mux := http.NewServeMux()
	r.HandleRoutes(mux)
	g := NewGateway("enabled")
	h := g.authMiddleware(mux)
	admin := sessionToken(User{Username: "admin", Role: RoleAdmin, Enabled: true})
	member := sessionToken(User{Username: "member", Role: RoleMember, Enabled: true})
	defer func() { sessionsMu.Lock(); delete(sessions, admin); delete(sessions, member); sessionsMu.Unlock() }()
	for _, tc := range []struct {
		id, token string
		want      int
	}{
		{a.ApplicationID, "", 401}, {a.ApplicationID, member, 403}, {linked.ApplicationID, admin, 409}, {"missing", admin, 404}, {a.ApplicationID, admin, 200}, {a.ApplicationID, admin, 200},
	} {
		req := httptest.NewRequest("DELETE", "/api/registry/applications/"+tc.id, nil)
		if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: "lp_session", Value: tc.token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}
