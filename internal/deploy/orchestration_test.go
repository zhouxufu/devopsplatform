package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func deliveryFixture(t *testing.T) (*Store, *ReleaseManager, DeliveryRequest) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, err := range []error{
		st.InitializeEnvironments([]Environment{{Name: "test", DisplayName: "测试"}, {Name: "prod", DisplayName: "生产", Protected: true, Approvers: "alice,reviewer", RequireReason: true}}),
		st.UpsertService(Service{Name: "orders", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", ArtifactGlob: "*.jar"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, env := range []string{"test", "prod"} {
		if err := st.UpsertHost(Host{Name: env + "-1", Environment: env, Addr: "127.0.0.1:22", User: "deploy"}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertHostGroup(HostGroup{Name: env + "-group", Environment: env, Hosts: []string{env + "-1"}}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "orders", Environment: env, TargetType: "ssh-jar", RemoteDir: "/home/orders", StartCmd: "true", HostGroup: env + "-group", BatchSize: 1, RuntimeEnv: map[string]string{"API_KEY": "never-serialize-me"}}); err != nil {
			t.Fatal(err)
		}
	}
	a := &Artifact{Service: "orders", Version: "commit-one", Kind: "jar", URI: "/not-executed.jar", Digest: "sha256:abc"}
	if err := st.RegisterArtifact(a); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "发布订单", Mode: "fixed", Services: []string{"orders"}, Owner: "alice", Nodes: []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "发布应用"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	return st, NewReleaseManager(st, nil), DeliveryRequest{TemplateID: tpl.ID, Environment: "test", Reason: "测试交付", Artifacts: map[string]int64{"orders": a.ID}}
}

func TestDeliveryVersionScopeDeduplicatesCodeOnly(t *testing.T) {
	st, m, req := deliveryFixture(t)
	version := &ReleaseVersion{Version: "v1", Title: "两个 BUG 同一次发布", Owner: "alice"}
	if err := st.CreateReleaseVersion(version); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for i := 0; i < 2; i++ {
		c := &Change{Kind: "code", Title: fmt.Sprint("BUG", i), Service: "orders", Reference: "feature/bug", Owner: "alice"}
		if err := st.CreateChange(c); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetChangeReady(c.ID, true); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	if err := st.SetReleaseVersionChanges(version.ID, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetReleaseVersionReady(version.ID); err != nil {
		t.Fatal(err)
	}
	tpl := &DeliveryTemplate{Name: "版本交付", Mode: "version", Owner: "alice", Nodes: []DeliveryNode{{ID: "deploy", Name: "发布服务", Kind: "deploy"}}}
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.TemplateID, req.VersionID = tpl.ID, version.ID
	p, _, err := st.ResolveDelivery(req, m)
	if err != nil || len(p.Blockers) != 0 || len(p.Services) != 1 {
		t.Fatalf("scope: %#v %v", p, err)
	}
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil || len(run.Steps) != 1 {
		t.Fatalf("duplicate deployment: %#v %v", run, err)
	}
	if len(st.ReleaseOrders("", 100)) != 0 {
		t.Fatal("submission started a deployment")
	}
}

func TestDeliveryApprovalSnapshotAndSecrets(t *testing.T) {
	st, m, req := deliveryFixture(t)
	req.Environment = "prod"
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "pending_approval" {
		t.Fatal(run.Status)
	}
	if _, err = st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil {
		t.Fatal("deployed before approval")
	}
	for _, actor := range []string{"alice", "outsider"} {
		if _, err := st.ReviewDeliveryRun(run.ID, actor, run.Digest, true); err == nil {
			t.Fatalf("%s approved", actor)
		}
	}
	if _, err = st.ReviewDeliveryRun(run.ID, "reviewer", "wrong-digest", true); err == nil {
		t.Fatal("wrong digest approved")
	}
	if _, err = st.ReviewDeliveryRun(run.ID, "reviewer", run.Digest, true); err != nil {
		t.Fatal(err)
	}
	tpl := st.DeliveryTemplate(req.TemplateID)
	tpl.Name = "edited after submission"
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	saved := st.DeliveryRun(run.ID)
	if saved.Plan.Template.Name != run.Plan.Template.Name || saved.Digest != run.Digest {
		t.Fatal("plan changed after template edit")
	}
	encoded, _ := json.Marshal(saved)
	if strings.Contains(string(encoded), "never-serialize-me") || strings.Contains(string(encoded), "enc:v1:") {
		t.Fatal("API leaked environment secrets")
	}
	var plan, secrets string
	if err := st.db.QueryRow(`SELECT plan,env_secrets FROM delivery_runs WHERE id=?`, run.ID).Scan(&plan, &secrets); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan+secrets, "never-serialize-me") || !strings.Contains(secrets, "enc:v1:") {
		t.Fatal("secrets not encrypted in database")
	}
	changed := st.GetServiceEnvironment("orders", "prod")
	changed.RemoteDir = "/different"
	if err := st.UpsertServiceEnvironment(*changed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("launched changed target") }); err == nil || !strings.Contains(err.Error(), "配置") {
		t.Fatalf("changed target accepted: %v", err)
	}
}

func TestDeliveryStepwiseExecutionAndDuplicateClicks(t *testing.T) {
	st, m, req := deliveryFixture(t)
	req.Nodes = []DeliveryNode{{ID: "check", Kind: "manual", Name: "确认窗口", Instructions: "确认发布窗口已开放"}, {ID: "deploy", Kind: "deploy", Name: "应用发布"}}
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil {
		t.Fatal("manual gate skipped")
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", true, m); err != nil {
		t.Fatal(err)
	}
	var launches atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { launches.Add(1) })
		}()
	}
	wg.Wait()
	if launches.Load() != 1 {
		t.Fatalf("launched %d times", launches.Load())
	}
	current := st.DeliveryRun(run.ID)
	child := st.GetReleaseOrder(current.Steps[1].ReleaseID)
	if child == nil || child.ArtifactID != req.Artifacts["orders"] || child.Target.RemoteDir != "/home/orders" {
		t.Fatalf("missing snapshot child: %#v", child)
	}
	st.SetReleaseStatus(child.ID, "success", "", true)
	if _, err := st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("duplicate child") }); err != nil {
		t.Fatal(err)
	}
	result, err := st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("duplicate child") })
	if err != nil || result.Status != "success" {
		t.Fatalf("completion: %#v %v", result, err)
	}
}

func TestDeliveryUnsupportedExecutorsFailClosed(t *testing.T) {
	st, m, req := deliveryFixture(t)
	for _, kind := range []string{"mysql", "nacos", "data-sync", "redis"} {
		req.Nodes = []DeliveryNode{{ID: "extra", Name: "外部变更", Kind: kind}, {ID: "deploy", Name: "发布", Kind: "deploy"}}
		p, _, err := st.ResolveDelivery(req, m)
		if err != nil || len(p.Blockers) == 0 {
			t.Fatalf("%s preview is not blocked: %v", kind, err)
		}
		if _, err := st.CreateDeliveryRun(req, "alice", m); err == nil {
			t.Fatalf("%s submitted", kind)
		}
	}
	req.Nodes = []DeliveryNode{{ID: "first", Name: "发布1", Kind: "deploy"}, {ID: "second", Name: "发布2", Kind: "deploy"}}
	if _, err := st.CreateDeliveryRun(req, "alice", m); err == nil {
		t.Fatal("same application scheduled twice")
	}
	if len(st.DeliveryRuns()) != 0 {
		t.Fatal("invalid submission persisted")
	}
}

func TestDeliveryRunCASAndAPIWriteOwnership(t *testing.T) {
	st, m, req := deliveryFixture(t)
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	stale := st.DeliveryRun(run.ID)
	if err := st.saveDeliveryRun(run); err != nil {
		t.Fatal(err)
	}
	if err := st.saveDeliveryRun(stale); err != ErrRunConflict {
		t.Fatalf("stale overwrite: %v", err)
	}
	mux := http.NewServeMux()
	registerDeliveryRoutes(mux, st, m)
	call := func(path, user, role string, body any) *httptest.ResponseRecorder {
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(encoded))
		r.Header.Set("X-User", user)
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call(fmt.Sprintf("/api/delivery/runs/%d/advance", run.ID), "outsider", "member", map[string]bool{}); w.Code == 200 {
		t.Fatal("non-owner advanced run")
	}
	req.Environment = "prod"
	prod, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(fmt.Sprintf("/api/delivery/runs/%d/approve", prod.ID), "outsider", "admin", map[string]string{"digest": prod.Digest}); w.Code == 200 {
		t.Fatal("admin bypassed configured reviewers")
	}
}

func TestDeliveryFailureStopsLaterNodes(t *testing.T) {
	st, m, req := deliveryFixture(t)
	req.Nodes = []DeliveryNode{{ID: "deploy", Kind: "deploy", Name: "发布"}, {ID: "post", Kind: "manual", Name: "上线验收", Instructions: "验收功能"}}
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	run, err = st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) {})
	if err != nil {
		t.Fatal(err)
	}
	st.SetReleaseStatus(run.Steps[0].ReleaseID, "failed", "", true)
	run, err = st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("launched after failure") })
	if err != nil || run.Status != "running" || run.Steps[1].Status != "pending" {
		t.Fatalf("failure propagation: %#v %v", run, err)
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", true, m); err == nil {
		t.Fatal("failed run advanced")
	}
}

func TestDeliveryChangedHostAndNewProtectionRequireNewApproval(t *testing.T) {
	st, m, req := deliveryFixture(t)
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	host := st.GetHost("test-1")
	host.Addr = "127.0.0.2:22"
	if err := st.UpsertHost(*host); err != nil {
		t.Fatal(err)
	}
	if _, err := st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("changed host launched") }); err == nil || !strings.Contains(err.Error(), "地址") {
		t.Fatalf("changed host accepted: %v", err)
	}
	if err := st.UpsertEnvironment(Environment{Name: "test", DisplayName: "测试", Protected: true, Approvers: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceDeliveryRun(run.ID, "alice", false, m); err == nil {
		t.Fatal("new protection bypassed")
	}
}

func TestDeliveryCompetingRunsUseSharedReleaseReservation(t *testing.T) {
	st, m, req := deliveryFixture(t)
	a, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	var launches atomic.Int64
	var wg sync.WaitGroup
	for _, id := range []int64{a.ID, b.ID} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			_, _ = st.advanceDeliveryRun(id, "alice", false, m, func(int64) { launches.Add(1) })
		}(id)
	}
	wg.Wait()
	if launches.Load() != 1 || len(st.ReleaseOrders("orders", 100)) != 1 {
		t.Fatal("two runs launched the same application concurrently")
	}
}

func TestDeliveryInterruptedChildStopsWorkflow(t *testing.T) {
	st, m, req := deliveryFixture(t)
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) {}); err != nil {
		t.Fatal(err)
	}
	m.Recover()
	result, err := st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("restart repeated deployment") })
	if err != nil || result.Status != "failed" {
		t.Fatalf("interrupted child not failed: %#v %v", result, err)
	}
}

func TestDeliveryMultipleApplicationsAdvanceInOrder(t *testing.T) {
	st, m, req := deliveryFixture(t)
	if err := st.UpsertService(Service{Name: "billing", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", ArtifactGlob: "*.jar"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertServiceEnvironment(ServiceEnvironment{Service: "billing", Environment: "test", TargetType: "ssh-jar", RemoteDir: "/home/billing", StartCmd: "true", HostGroup: "test-group", BatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	artifact := &Artifact{Service: "billing", Version: "commit-two", Kind: "jar", URI: "/not-executed-billing.jar", Digest: "sha256:billing"}
	if err := st.RegisterArtifact(artifact); err != nil {
		t.Fatal(err)
	}
	tpl := st.DeliveryTemplate(req.TemplateID)
	tpl.Services = append(tpl.Services, "billing")
	if err := st.SaveDeliveryTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	req.Artifacts["billing"] = artifact.ID
	run, err := st.CreateDeliveryRun(req, "alice", m)
	if err != nil || len(run.Steps) != 2 {
		t.Fatalf("multi app plan: %#v %v", run, err)
	}
	for i := 0; i < 2; i++ {
		run, err = st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) {})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && run.Steps[1].ReleaseID != 0 {
			t.Fatal("later application started before first result")
		}
		child := st.GetReleaseOrder(run.Steps[i].ReleaseID)
		if child == nil || child.Service != run.Steps[i].Service {
			t.Fatal("wrong child application")
		}
		st.SetReleaseStatus(child.ID, "success", "", true)
		if _, err = st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("launch instead of confirming result") }); err != nil {
			t.Fatal(err)
		}
	}
	run, err = st.advanceDeliveryRun(run.ID, "alice", false, m, func(int64) { t.Fatal("extra deployment") })
	if err != nil || run.Status != "success" || len(st.ReleaseOrders("", 100)) != 2 {
		t.Fatalf("multi app completion: %#v %v", run, err)
	}
}
