package apitest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLoginFormIndependentOriginInjectionAndVerification(t *testing.T) {
	for _, injection := range []string{"header", "cookie", "query"} {
		t.Run(injection, func(t *testing.T) {
			var logins, business atomic.Int32
			authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logins.Add(1)
				if r.URL.Path != "/sign-in" || r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Error("wrong login target")
				}
				_ = r.ParseForm()
				if r.Form.Get("username") != "test-user" || r.Form.Get("password") != "p&a=ss+word" {
					t.Error("form substitution changed credentials")
				}
				io.WriteString(w, `{"data":{"token":"issued-secret","account":"tester-17"}}`)
			}))
			defer authServer.Close()
			businessServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				business.Add(1)
				if r.URL.Path != "/gateway/orders" {
					t.Error("lost prefix")
				}
				switch injection {
				case "header":
					if r.Header.Get("Authorization") != "Bearer issued-secret" {
						t.Error("lost token")
					}
				case "query":
					if r.URL.Query().Get("session") != "issued-secret" {
						t.Error("lost query token")
					}
				case "cookie":
					c, e := r.Cookie("session")
					if e != nil || c.Value != "issued-secret" {
						t.Error("lost cookie token")
					}
				}
				io.WriteString(w, `{"ok":true,"echo":"issued-secret"}`)
			}))
			defer businessServer.Close()
			vars := map[string]string{"USER": "test-user", "PASS": "p&a=ss+word"}
			a := Auth{Mode: "login", BaseURL: authServer.URL, Path: "/sign-in", BodyType: "form", Body: "username={{var.USER}}&password={{var.PASS}}", TokenPointer: "/data/token", IdentityPointer: "/data/account", Injection: injection, Name: "session", Refresh: "per_request", ExpiresIn: 300}
			if injection == "header" {
				a.Prefix = "Bearer "
			}
			r := Request{Method: "GET", Path: "/orders", AuthenticationOnly: true}
			result := Perform(context.Background(), http.DefaultClient, businessServer.URL+"/gateway", r, a, vars, nil)
			if result.Error != "" || result.Authentication == nil || !result.Authentication.IdentityVerified || result.Authentication.Identity != "tester-17" || result.URL != authServer.URL+"/sign-in" || logins.Load() != 1 || business.Load() != 0 {
				t.Fatalf("verification sent business request or failed: %+v", result)
			}
			r.AuthenticationOnly = false
			for i := 0; i < 2; i++ {
				result = Perform(context.Background(), http.DefaultClient, businessServer.URL+"/gateway", r, a, vars, nil)
				if result.Error != "" {
					t.Fatal(result.Error)
				}
				raw, _ := json.Marshal(result)
				if strings.Contains(string(raw), "issued-secret") || strings.Contains(string(raw), "p&a") {
					t.Fatal("credentials escaped result")
				}
			}
			if logins.Load() != 3 || business.Load() != 2 {
				t.Fatal("unexpected cache or replay")
			}
		})
	}
}
func TestCredentialReferencesAndUnsupportedRefreshRejected(t *testing.T) {
	safe := Auth{Mode: "login", Path: "/login", Body: `{"username":"{{var.USER}}","password":"{{var.PASS}}","grant_type":"password"}`, TokenPointer: "/data/token"}
	if _, e := ReferenceAuth(safe); e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{`{"username":"literal-person","password":"private"}`, `{"unusual_key":"hidden-credential"}`, `{"password":`} {
		a := safe
		a.Body = body
		redacted, e := ReferenceAuth(a)
		if e == nil || strings.Contains(redacted.Body, "hidden-credential") || strings.Contains(redacted.Body, "private") {
			t.Fatal("unsafe auth exported", redacted, e)
		}
	}
	safe.Refresh = "on_401"
	if ValidateAuth(safe) == nil {
		t.Fatal("automatic request replay accepted")
	}
	for _, r := range []Request{{Path: "/orders?token=secret"}, {Path: "/", Body: `{"password":"private"}`}, {Path: "/", Headers: map[string]string{"Authorization": "Bearer private"}}} {
		if ValidateReferences(r) == nil {
			t.Fatal("plaintext request credentials accepted")
		}
	}
	if e := ValidateReferences(Request{Path: "/orders", Body: `{"title":"normal business data"}`}); e != nil {
		t.Fatal(e)
	}
}

func TestReferenceAuthDoesNotModifyOriginalAndRedactsNonStringCredentials(t *testing.T) {
	original := Auth{Headers: map[string]string{"Authorization": "private"}, Body: `{"password":123456,"nested":["secret"]}`}
	safe, err := ReferenceAuth(original)
	if err == nil || original.Headers["Authorization"] != "private" || strings.Contains(safe.Body, "123456") || strings.Contains(safe.Body, "secret") {
		t.Fatal("reference redaction changed original or leaked", safe, err)
	}
}

func TestReferenceAuthProtectsCustomCredentialHeaders(t *testing.T) {
	for _, header := range []string{"X-Session", "X-Custom-Auth", "Authorization"} {
		if a, err := ReferenceAuth(Auth{Headers: map[string]string{header: "literal-value"}}); err == nil || a.Headers[header] != "[REDACTED]" {
			t.Fatal("custom credential exposed", a, err)
		}
	}
	if _, err := ReferenceAuth(Auth{Headers: map[string]string{"Authorization": "Bearer {{var.LOGIN_KEY}}", "Content-Type": "application/json"}}); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceAuthHidesLegacyLoginQuery(t *testing.T) {
	a, err := ReferenceAuth(Auth{Mode: "login", Path: "/login?custom_login_key=private-value", Body: "{}"})
	if err == nil || strings.Contains(a.Path, "private-value") {
		t.Fatal("login query credentials leaked", a, err)
	}
}

func TestMaskedLoginIdentityRetainsResponseEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"token":"issued-private","account":"bound-person"}`)
	}))
	defer server.Close()
	a := Auth{Mode: "login", BaseURL: server.URL, Path: "/login", Body: `{"username":"{{var.USER}}"}`, TokenPointer: "/token", IdentityPointer: "/account"}
	r := Perform(context.Background(), http.DefaultClient, "https://business.invalid", Request{Method: "GET", Path: "/", AuthenticationOnly: true}, a, map[string]string{"USER": "bound-person"}, nil)
	if r.Error != "" || r.Authentication == nil || !r.Authentication.IdentityVerified || r.Authentication.Identity == "bound-person" || r.URL != server.URL+"/login" {
		t.Fatal("identity evidence lost or credentials exposed", r)
	}
	a.IdentityPointer = a.TokenPointer
	if ValidateAuth(a) == nil {
		t.Fatal("token presented as user identity")
	}
}
