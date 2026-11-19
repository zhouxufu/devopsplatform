package platform

import (
	"net/http/httptest"
	"testing"
)

func TestDeploymentAgentDataPlanePathsAreNarrow(t *testing.T) {
	for _, v := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/plugins/deploy/api/deployment-agent/tasks", true},
		{"POST", "/api/plugins/deploy/api/deployment-agent/reports", true},
		{"POST", "/api/plugins/deploy/api/deployment-agent/enrollment", true},
		{"GET", "/api/plugins/deploy/api/deployment-agent/enrollment", false},
		{"GET", "/api/plugins/deploy/api/deployment-agent/tasks/deploy-1/artifact", true},
		{"POST", "/api/plugins/deploy/api/deployment-agent/tasks", false},
		{"GET", "/api/plugins/deploy/api/deployment-agent/tasks/../artifact", false},
		{"GET", "/api/plugins/deploy/api/deployment-agent/tasks/deploy-1/artifact/extra", false},
		{"POST", "/api/plugins/deploy/api/deployment-agents", false},
		{"DELETE", "/api/plugins/deploy/api/deployment-agents/x", false},
		{"GET", "/api/plugins/deploy/api/hosts", false},
	} {
		if got := deploymentAgentDataPath(httptest.NewRequest(v.method, v.path, nil)); got != v.want {
			t.Errorf("%s %s = %v", v.method, v.path, got)
		}
	}
	if !requiresAdmin(httptest.NewRequest("POST", "/api/plugins/deploy/api/deployment-agents", nil)) || !requiresAdmin(httptest.NewRequest("DELETE", "/api/plugins/deploy/api/deployment-agents/x", nil)) {
		t.Fatal("Agent authorization administration is not admin-only")
	}
}
