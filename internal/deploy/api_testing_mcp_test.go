package deploy

import (
	"encoding/json"
	"io"
	"logplatform/pkg/apitest"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAPITestingMCPEntriesCredentialDigestAndLoginOnly(t *testing.T) {
	st, h := apiTestingFixture(t)
	sv := st.GetService("orders")
	p, _ := st.apiTestingProfile(sv.ApplicationID, "test")
	p.Entries = []apiTestEntry{{Key: "direct", Kind: "direct", Label: "直连", BaseURL: "http://192.0.2.60:8080", Auth: apitest.Auth{Mode: "none"}}, {Key: "gateway", Kind: "gateway", Label: "网关", BaseURL: "http://192.0.2.70", PathPrefix: "/gateway/orders", Auth: apitest.Auth{Mode: "login", BaseURL: "http://192.0.2.80", Path: "/login", BodyType: "form", Body: "password={{var.API_PASSWORD}}", TokenPointer: "/token", IdentityPointer: "/account", Injection: "query", Name: "session"}}}
	w := apiTestingRequest(h, "PUT", "/api/api-testing/mcp/profile?service=orders&environment=test", "alice", "admin", p)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = apiTestingRequest(h, "GET", "/api/api-testing/mcp/context?service=orders&environment=test", "alice", "admin", nil)
	var context map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &context)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture-password") {
		t.Fatal(w.Body.String())
	}
	var digest string
	_ = json.Unmarshal(context["variables_digest"], &digest)
	var targets []apiTestTarget
	_ = json.Unmarshal(context["targets"], &targets)
	var gateway apiTestTarget
	for _, target := range targets {
		if target.Key == "endpoint:gateway" {
			gateway = target
		}
	}
	if gateway.BaseURL != "http://192.0.2.70/gateway/orders" || gateway.LoginURL != "http://192.0.2.80/login" {
		t.Fatal(gateway)
	}
	var logins, business atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			logins.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("password") != "fixture-password" {
				t.Error("lost bound credential")
			}
			io.WriteString(w, `{"token":"fixture-issued-token","account":"synthetic-alice"}`)
			return
		}
		business.Add(1)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	st.apiTests.client = apiTestingClient(upstream)
	input := apiTestInput{RequestID: "mcp-auth-only-test-0001", Name: "验证认证", Environment: "test", TargetKey: gateway.Key, TargetFingerprint: gateway.Fingerprint, ExpectedVariablesDigest: digest, VerifyAuth: true, Request: apitest.Request{Method: "GET", Path: "/"}}
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/runs?service=orders&environment=test", "alice", "member", input)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var run apiTestRun
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	done := waitAPITest(t, st, "alice", run.ID)
	if !done.VerifyAuth || done.Result.URL != "http://192.0.2.80/login" || done.Status != "success" || done.Result.Authentication == nil || !done.Result.Authentication.IdentityVerified || business.Load() != 0 || logins.Load() != 1 {
		t.Fatalf("login-only violated %+v", done)
	}
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/runs?service=orders&environment=test", "alice", "member", input)
	if w.Code != 200 || logins.Load() != 1 {
		t.Fatal("duplicate request replayed")
	}
	input.RequestID = "mcp-auth-only-test-0002"
	input.ExpectedVariablesDigest = "stale"
	w = apiTestingRequest(h, "POST", "/api/api-testing/mcp/runs?service=orders&environment=test", "alice", "member", input)
	if w.Code == 200 || logins.Load() != 1 {
		t.Fatal("stale variables executed")
	}
	w = apiTestingRequest(h, "GET", "/api/api-testing/mcp/context?service=orders&environment=test", "alice", "member", nil)
	if strings.Contains(w.Body.String(), `"profile"`) {
		t.Fatal("member read admin profile")
	}
	p.Revision++
	p.Entries[1].Auth.Body = "password=literal-secret"
	w = apiTestingRequest(h, "PUT", "/api/api-testing/mcp/profile?service=orders&environment=test", "alice", "admin", p)
	if w.Code == 200 {
		t.Fatal("literal credential saved")
	}
	// Existing default remains usable; named entry configuration never rewrites history.
	original, e := st.apiTests.load(sv.ApplicationID, "alice", run.ID)
	if e != nil || original.Target.BaseURL != gateway.BaseURL {
		t.Fatal("history changed")
	}
}

func TestAPITestTargetNeverExportsLegacyLoginURLSecrets(t *testing.T) {
	st, _ := apiTestingFixture(t)
	sv := st.GetService("orders")
	p, _ := st.apiTestingProfile(sv.ApplicationID, "test")
	p.Auth = apitest.Auth{Mode: "login", Path: "/login?custom_key=legacy-private", Body: "{}", TokenPointer: "/token"}
	targets := st.apiTestTargets(sv, st.GetServiceEnvironment("orders", "test"), p)
	raw, _ := json.Marshal(targets)
	if strings.Contains(string(raw), "legacy-private") || targets[0].CredentialReferencesValid {
		t.Fatal("legacy login URL exposed", string(raw))
	}
}
