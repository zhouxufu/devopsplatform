package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentPolicyUsesEnvironmentContractAndDiagnosesBinding(t *testing.T) {
	s, _, _ := deliveryFixture(t)
	sv := s.GetService("orders")
	sv.Branch = "master-house-claim"
	sv.BuildTool = "maven"
	sv.MavenGoals = "package"
	sv.MavenProperties = "build.label=hidden-build-secret"
	if err := s.UpsertService(*sv); err != nil {
		t.Fatal(err)
	}
	p, _ := s.deliveryPolicy("test")
	p.Defaults.Applications = map[string]ApplicationDeliveryDefault{"orders": {CodePolicy: CodePolicy{Mode: "integration", TargetBranch: "master-dev-optimize"}, BuildVariant: BuildVariant{Name: "test", MavenProfiles: "test"}, ArtifactSource: "build"}}
	if err := s.saveDeliveryPolicy("test", p); err != nil {
		t.Fatal(err)
	}
	v, err := s.applicationCodePolicy("orders", "test")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), "master-dev-optimize") || strings.Contains(string(b), "hidden-build-secret") || v["branch_type"] != "fixed" {
		t.Fatal(string(b))
	}
	diagnostic := v["workflow_binding"].(map[string]any)
	if diagnostic["status"] != "unbound" || diagnostic["policy_revision"] != int64(1) {
		t.Fatal(diagnostic)
	}
	if _, err = s.applicationCodePolicy("orders", "missing"); err == nil {
		t.Fatal("unknown environment")
	}
}
