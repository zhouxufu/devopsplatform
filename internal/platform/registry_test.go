package platform

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeploymentStateIsSeparatedByEnvironment(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.db.Close()
	for _, state := range []DeploymentState{
		{Service: "orders", Environment: "test", Version: "v1", Hosts: []string{"test-a"}},
		{Service: "orders", Environment: "prod", Version: "v9", Hosts: []string{"prod-a"}},
		{Service: "orders", Environment: "prod", Version: "v9", Hosts: []string{"prod-b"}},
	} {
		if err := r.RecordDeployment(state); err != nil {
			t.Fatal(err)
		}
	}
	states := r.DeploymentStates()
	if len(states) != 2 {
		t.Fatalf("states = %#v", states)
	}
	for _, state := range states {
		if state.Environment == "prod" && (state.Version != "v9" || len(state.Hosts) != 2) {
			t.Fatalf("prod state = %#v", state)
		}
		if state.Environment == "test" && state.Version != "v1" {
			t.Fatalf("test state = %#v", state)
		}
	}
	if err := r.RecordDeployment(DeploymentState{Service: "orders", Environment: "prod", Version: "v10", Hosts: []string{"prod-a"}}); err != nil {
		t.Fatal(err)
	}
	for _, state := range r.DeploymentStates() {
		if state.Environment == "prod" && (state.Version != "v10" || len(state.Hosts) != 1) {
			t.Fatalf("new version should reset host rollout set: %#v", state)
		}
	}
}

func TestEmptyApplicationListUsesJSONArrayContract(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	list, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("empty application list must be a non-nil empty slice: %#v", list)
	}
}

func TestObservedApplicationKeepsOneIdentityAcrossSources(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fromLogs, err := r.Observe("orders", "logs", "")
	if err != nil {
		t.Fatal(err)
	}
	if fromLogs.ApplicationID == "" || fromLogs.Status != "discovered" {
		t.Fatalf("logs observation = %#v", fromLogs)
	}
	fromOTel, err := r.Observe("orders", "otel", "")
	if err != nil {
		t.Fatal(err)
	}
	fromDeploy, err := r.Observe("orders", "deploy", "legacy-deploy-id")
	if err != nil {
		t.Fatal(err)
	}
	if fromOTel.ApplicationID != fromLogs.ApplicationID || fromDeploy.ApplicationID != fromLogs.ApplicationID {
		t.Fatalf("identity changed: logs=%q otel=%q deploy=%q", fromLogs.ApplicationID, fromOTel.ApplicationID, fromDeploy.ApplicationID)
	}
	if fromDeploy.Status != "managed" || !containsString(fromDeploy.Sources, "logs") || !containsString(fromDeploy.Sources, "otel") || !containsString(fromDeploy.Sources, "deploy") {
		t.Fatalf("merged application = %#v", fromDeploy)
	}
}

func TestObservedApplicationCanMatchExistingRequestedID(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	first, err := r.Observe("old-name", "deploy", "app-stable")
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := r.Observe("new-name", "logs", "app-stable")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ApplicationID != first.ApplicationID {
		t.Fatalf("requested identity was not reused: %#v %#v", first, renamed)
	}
}

func TestRegistrySnapshotRestorePreservesStableIdentity(t *testing.T) {
	source, err := OpenRegistry(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	app, err := source.Observe("orders", "logs", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Observe("orders", "deploy", app.ApplicationID); err != nil {
		t.Fatal(err)
	}
	if err := source.RecordDeployment(DeploymentState{Service: "orders", Environment: "test", Version: "v1", Hosts: []string{"test-a"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	target, err := OpenRegistry(filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := target.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := target.Restore(snapshot); err != nil { // migration is retry-safe
		t.Fatal(err)
	}
	restored, err := target.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Services) != 1 || restored.Services[0].ApplicationID != app.ApplicationID || restored.Services[0].Status != "managed" {
		t.Fatalf("restored services = %#v", restored.Services)
	}
	if len(restored.Deployments) != 1 || len(restored.Identities) != 2 {
		t.Fatalf("restored snapshot = %#v", restored)
	}
}

func TestRegistryRejectsUnsupportedDriver(t *testing.T) {
	if _, err := OpenRegistryDatabase(RegistryConfig{Driver: "postgres", DSN: "ignored"}); err == nil {
		t.Fatal("unsupported driver should fail")
	}
}

func TestDetachDeploySourceKeepsHistoricalIdentity(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fromLogs, err := r.Observe("orders", "logs", "")
	if err != nil {
		t.Fatal(err)
	}
	managed, err := r.Observe("orders", "deploy", fromLogs.ApplicationID)
	if err != nil || managed.Status != "managed" {
		t.Fatalf("managed application = %#v, %v", managed, err)
	}
	detached, err := r.DetachSource("orders", "deploy", managed.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if detached.Status != "discovered" || containsString(detached.Sources, "deploy") || !containsString(detached.Sources, "logs") {
		t.Fatalf("detached application = %#v", detached)
	}
	recreated, err := r.Observe("orders", "deploy", "")
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ApplicationID != managed.ApplicationID || recreated.Status != "managed" {
		t.Fatalf("recreated application lost identity: %#v", recreated)
	}
}

func TestDetachLastSourceArchivesApplication(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	managed, err := r.Observe("orders", "deploy", "")
	if err != nil {
		t.Fatal(err)
	}
	detached, err := r.DetachSource("orders", "deploy", managed.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if detached.Status != "archived" || len(detached.Sources) != 0 {
		t.Fatalf("detached application = %#v", detached)
	}
	active, err := r.ListActive()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("archived application leaked into active list: %#v", active)
	}
	all, err := r.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("historical identity was deleted: %#v, %v", all, err)
	}
}

func TestBusRetriesFailedDelivery(t *testing.T) {
	var attempts atomic.Int32
	delivered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		select {
		case delivered <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()
	b := NewBus()
	b.Subscribe([]string{"logs.metrics.*"}, srv.URL)
	if queued := b.Publish(BusEvent{Topic: "logs.metrics.minute", From: "logs"}); queued != 1 {
		t.Fatalf("queued = %d", queued)
	}
	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatalf("delivery not retried, attempts=%d", attempts.Load())
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}
