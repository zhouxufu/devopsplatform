package deploy

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestDirectEnvironmentHostsPersistWithoutGroups(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, env := range []string{"test", "prod"} {
		if err := st.UpsertEnvironment(Environment{Name: env, DisplayName: env}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertService(Service{Name: "app", ArtifactType: "image"}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []Host{{Name: "a", Environment: "test", Addr: "a"}, {Name: "b", Environment: "test", Addr: "b"}, {Name: "prod", Environment: "prod", Addr: "prod"}} {
		if err := st.UpsertHost(host); err != nil {
			t.Fatal(err)
		}
	}
	target := ServiceEnvironment{Service: "app", Environment: "test", TargetType: "docker-host", RemoteDir: "/", Hosts: []string{"b", "a"}}
	if err := st.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	got := st.GetServiceEnvironment("app", "test")
	if got.HostGroup != "" || !reflect.DeepEqual(got.Hosts, target.Hosts) || len(st.HostGroups()) != 0 {
		t.Fatal("direct targets require no implicit group", got)
	}
	if err := st.DeleteHost("a"); err == nil {
		t.Fatal("deleted referenced host")
	}
	if err := st.UpsertHost(Host{Name: "a", Environment: "prod", Addr: "a"}); err == nil {
		t.Fatal("moved referenced host across environments")
	}
	for _, selection := range [][]string{nil, {"missing"}, {"a", "a"}, {"prod"}} {
		invalid := target
		invalid.Hosts = selection
		if err := st.UpsertServiceEnvironment(invalid); err == nil {
			t.Fatal("accepted invalid selection", selection)
		}
		if !reflect.DeepEqual(st.GetServiceEnvironment("app", "test").Hosts, target.Hosts) {
			t.Fatal("failed save altered old bindings")
		}
	}
	target.Hosts = []string{"b"}
	if err := st.UpsertServiceEnvironment(target); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteHost("a"); err != nil {
		t.Fatal("removed direct link still referenced", err)
	}
}

func TestDirectHostsReleaseSnapshotAndApproval(t *testing.T) {
	st, m, request := deliveryFixture(t)
	target := st.GetServiceEnvironment("orders", "prod")
	target.HostGroup = ""
	target.Hosts = []string{"prod-1"}
	if err := st.UpsertServiceEnvironment(*target); err != nil {
		t.Fatal(err)
	}
	// This creates only a pending approval record. It must never start SSH.
	order, err := m.CreateArtifact("orders", request.Artifacts["orders"], "prod", "direct target", "developer")
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != "pending_approval" || order.Target.HostGroup != "" || !reflect.DeepEqual(order.Target.Hosts, []string{"prod-1"}) {
		t.Fatal(order)
	}
	target.Hosts = []string{"prod-2"}
	if err := st.UpsertHost(Host{Name: "prod-2", Environment: "prod", Addr: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceEnvironment(*target); err != nil {
		t.Fatal(err)
	}
	frozen := st.GetReleaseOrder(order.ID)
	if !reflect.DeepEqual(frozen.Target.Hosts, []string{"prod-1"}) {
		t.Fatal("existing reviewed snapshot changed", frozen)
	}
}

func TestDirectHostBindingSerializesEnvironmentChanges(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, env := range []string{"test", "prod"} {
		if err := st.UpsertEnvironment(Environment{Name: env, DisplayName: env}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("app-%d", i)
		if err := st.UpsertService(Service{Name: name, ArtifactType: "image"}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertHost(Host{Name: name, Environment: "test", Addr: "host"}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			results <- st.UpsertServiceEnvironment(ServiceEnvironment{Service: name, Environment: "test", TargetType: "docker-host", RemoteDir: "/", Hosts: []string{name}})
		}()
		go func() {
			defer wg.Done()
			<-start
			results <- st.UpsertHost(Host{Name: name, Environment: "prod", Addr: "host"})
		}()
		close(start)
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			}
		}
		if success != 1 {
			t.Fatal("expected one competing update to succeed", success)
		}
		if target := st.GetServiceEnvironment(name, "test"); target != nil && st.GetHost(name).Environment != "test" {
			t.Fatal("cross-environment direct binding committed")
		}
	}
}

func TestDirectHostLegacyInvalidScopeCannotPublish(t *testing.T) {
	st, m, _ := deliveryFixture(t)
	target := st.GetServiceEnvironment("orders", "prod")
	target.HostGroup = ""
	target.Hosts = []string{"prod-1"}
	if err := st.UpsertServiceEnvironment(*target); err != nil {
		t.Fatal(err)
	}
	// Simulate an invalid old metadata row; public edits cannot move a bound host.
	if _, err := st.db.Exec(`UPDATE hosts SET environment='test' WHERE name='prod-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ValidateTarget("orders", "prod", "reason"); err == nil {
		t.Fatal("invalid historical host scope accepted")
	}
}
