package deploy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationDeliveryDefaultsAtomicAndScoped(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	if err := s.UpsertService(Service{Name: "portal", ArtifactType: "jar", BuildTool: "command", BuildCmd: "true", ArtifactGlob: "*.jar"}); err != nil {
		t.Fatal(err)
	}
	policy, _ := s.deliveryPolicy("test")
	policy.Defaults = DeliveryDefaults{ArtifactSource: "existing", Applications: map[string]ApplicationDeliveryDefault{"orders": {ArtifactSource: "existing", CodePolicy: CodePolicy{Mode: "integration", TargetBranch: "old-test"}}, "portal": {ArtifactSource: "existing"}}}
	if err := s.saveDeliveryPolicy("test", policy); err != nil {
		t.Fatal(err)
	}
	state, err := s.applicationDeliveryDefault("orders", "test")
	if err != nil || state.Value.CodePolicy.TargetBranch != "old-test" || state.Revision != 1 {
		t.Fatal("existing defaults were not loaded", state, err)
	}
	beforeProd, _ := s.deliveryPolicy("prod")
	target := *s.GetServiceEnvironment("orders", "test")
	target.RemoteDir = "/home/new-orders"
	value := ApplicationDeliveryDefault{ArtifactSource: "build", CodePolicy: CodePolicy{Mode: "integration", BaseBranch: "ignored", TargetBranch: "test"}}
	patch := &applicationDefaultPatch{Revision: state.Revision, Value: &value}
	if err := s.upsertServiceEnvironment(target, patch); err != nil {
		t.Fatal(err)
	}
	current, _ := s.deliveryPolicy("test")
	if current.Revision != 2 || current.Defaults.ArtifactSource != "existing" || current.Defaults.Applications["orders"].CodePolicy.BaseBranch != "" || current.Defaults.Applications["orders"].CodePolicy.TargetBranch != "test" {
		t.Fatal(current)
	}
	if current.Defaults.Applications["portal"] != policy.Defaults.Applications["portal"] {
		t.Fatal("other application's defaults changed")
	}
	if got := s.GetServiceEnvironment("orders", "test"); got.RemoteDir != target.RemoteDir || len(got.RuntimeEnvKeys) != 1 {
		t.Fatal("destination or saved runtime variables lost", got)
	}
	afterProd, _ := s.deliveryPolicy("prod")
	if digestJSON(beforeProd) != digestJSON(afterProd) {
		t.Fatal("other environment modified")
	}
	// A stale editor cannot save either half of the configuration.
	target.RemoteDir = "/home/must-not-save"
	if err := s.upsertServiceEnvironment(target, patch); err == nil {
		t.Fatal("stale policy accepted")
	}
	if s.GetServiceEnvironment("orders", "test").RemoteDir != "/home/new-orders" {
		t.Fatal("target partially saved on policy conflict")
	}
	bad := value
	bad.ArtifactSource = "untrusted"
	if s.upsertServiceEnvironment(target, &applicationDefaultPatch{Revision: 2, Value: &bad}) == nil {
		t.Fatal("invalid default accepted")
	}
	// Reset affects only the selected application, not environment defaults.
	if err := s.upsertServiceEnvironment(*s.GetServiceEnvironment("orders", "test"), &applicationDefaultPatch{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	reset, _ := s.applicationDeliveryDefault("orders", "test")
	if reset.Value != nil || reset.ArtifactSource != "existing" || reset.Revision != 3 {
		t.Fatal(reset)
	}
}

func TestApplicationDeliveryDefaultsRetainApprovalGates(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	p, _ := s.deliveryPolicy("prod")
	p.Stages = []DeliveryNode{{Name: "测试审批", Kind: "approval", Approvers: []string{"reviewer"}, ApprovalMode: "any"}}
	p.Defaults.StrategyKind = "standard"
	if err := s.saveDeliveryPolicy("prod", p); err != nil {
		t.Fatal(err)
	}
	p, _ = s.deliveryPolicy("prod")
	stages := digestJSON(p.Stages)
	value := ApplicationDeliveryDefault{ArtifactSource: "existing", CodePolicy: CodePolicy{Mode: "isolated", BaseBranch: "main", TargetBranch: "release/{version}"}}
	if err := s.upsertServiceEnvironment(*s.GetServiceEnvironment("orders", "prod"), &applicationDefaultPatch{Revision: p.Revision, Value: &value}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.deliveryPolicy("prod")
	if digestJSON(got.Stages) != stages || got.Defaults.StrategyKind != "standard" || !s.GetEnvironment("prod").Protected {
		t.Fatal("application save weakened shared environment controls")
	}
}

func TestApplicationDeliveryDefaultsConcurrentPolicyCAS(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	value := ApplicationDeliveryDefault{ArtifactSource: "build"}
	before, after, err := s.prepareApplicationDefault("orders", "test", &applicationDefaultPatch{Value: &value})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.deliveryPolicy("test")
	p.Defaults.ArtifactSource = "existing"
	if err := s.saveDeliveryPolicy("test", p); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`UPDATE service_environments SET remote_dir=? WHERE service=? AND environment=?`, "/must-rollback", "orders", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeApplicationDefault(tx, "test", before, after); err == nil {
		t.Fatal("concurrent insert accepted")
	}
	tx.Rollback()
	if s.GetServiceEnvironment("orders", "test").RemoteDir != "/home/orders" {
		t.Fatal("transaction did not preserve destination")
	}
}

func TestApplicationDeliveryDefaultsAPIAndPermissions(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	mux := Routes(s, nil, NewLogHub(), "", nil)
	request := func(method, url, user, role string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, url, bytes.NewReader(raw))
		r.Header.Set("X-User", user)
		r.Header.Set("X-Role", role)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	url := "/api/services/orders/environments/test"
	if w := request("GET", url+"/delivery-default", "alice", "member", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"value":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", "/api/services/missing/environments/test/delivery-default", "alice", "admin", nil); w.Code == http.StatusOK {
		t.Fatal("unknown service accepted")
	}
	target := s.GetServiceEnvironment("orders", "test")
	raw, _ := json.Marshal(target)
	var input map[string]any
	json.Unmarshal(raw, &input)
	input["delivery_default"] = applicationDefaultPatch{Value: &ApplicationDeliveryDefault{ArtifactSource: "build", CodePolicy: CodePolicy{Mode: "integration", TargetBranch: "test"}}}
	if w := request("PUT", url, "alice", "member", input); w.Code != http.StatusForbidden {
		t.Fatal("member changed environment defaults", w.Code, w.Body.String())
	}
	if w := request("PUT", url, "alice", "admin", input); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("PUT", url, "alice", "admin", input); w.Code == http.StatusOK {
		t.Fatal("stale API update accepted")
	}
	state, _ := s.applicationDeliveryDefault("orders", "test")
	if state.Revision != 1 || state.Value.ArtifactSource != "build" {
		t.Fatal(state)
	}
}
