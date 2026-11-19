package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryPinsActualRemoteHead(t *testing.T) {
	seed, url, base := preparationRemote(t)
	source := preparationSource(t, seed, "feature/a", "a.txt", "A\n")
	preparationGitCommand(t, seed, "checkout", "dev")
	preparationGitCommand(t, seed, "merge", "--no-ff", "--no-edit", source)
	head := preparationGitCommand(t, seed, "rev-parse", "HEAD")
	preparationGitCommand(t, seed, "push", "origin", "dev")
	r := PreparedRepository{URL: url, BaseCommit: base, TargetBranch: "dev", ExpectedResultCommit: head, Policy: CodePolicy{Mode: "isolated"}, Sources: []PreparedSource{{Commit: source}}}
	if err := mergePreparationRepository(context.Background(), preparationTestSession(t), &r, true); err != nil {
		t.Fatal(err)
	}
	preparationFile(t, seed, "a.txt", "followup\n")
	preparationGitCommand(t, seed, "add", ".")
	preparationGitCommand(t, seed, "commit", "-m", "later")
	preparationGitCommand(t, seed, "push", "origin", "dev")
	if err := mergePreparationRepository(context.Background(), preparationTestSession(t), &r, true); err == nil || !strings.Contains(err.Error(), "确认的解决提交") {
		t.Fatal("unconfirmed head accepted", err)
	}
}
func TestMCPRecoveryGatewayRequiresRevisionAndCommits(t *testing.T) {
	st, _, _ := deliveryFixture(t)
	p := &CodePreparation{ID: "mcp-fixture", Owner: "local", State: "conflict", PreparationRunID: 101, Request: DeliveryRequest{Environment: "test"}, Repositories: []PreparedRepository{{Service: "orders"}}}
	if err := st.saveCodePreparation(p); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerDeliveryMCPRoutes(mux, st, nil, nil)
	// No ordinary route is registered: a valid request would reach that route (404),
	// while malformed/replayed requests must be rejected before delegating.
	for _, body := range []string{`{"confirm":true}`, `{"confirm":true,"expected_revision":999}`, `{"confirm":true,"expected_revision":1,"target_commits":["HEAD"]}`} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/delivery/mcp/code-preparations/mcp-fixture/resume", strings.NewReader(body)))
		if w.Code != 400 && w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if st.CodePreparation(p.ID).Revision != p.Revision {
		t.Fatal("gateway mutated task")
	}
}
