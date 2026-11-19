package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestZentaoPathInfoSessionLoginAndPositionalLists(t *testing.T) {
	for _, getResponse := range []string{"html", "404", "redirect", "empty-session"} {
		t.Run(getResponse, func(t *testing.T) {
			loginCalls, getCalls, pathSessions, bugs, users := 0, 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.String(), "secret-password") {
					t.Error("password in URL")
				}
				if r.URL.Path == "/zentao/index.php" {
					getCalls++
					if r.URL.Query().Get("f") != "getSessionID" {
						t.Error("retried login with GET route")
					}
					switch getResponse {
					case "404":
						w.WriteHeader(404)
					case "redirect":
						http.Redirect(w, r, "https://other.example/login", 302)
					case "empty-session":
						_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
					default:
						_, _ = w.Write([]byte(`<html><script>self.location='user-login.html'</script></html>`))
					}
					return
				}
				if r.URL.RawQuery != "" {
					t.Error("PATHINFO args must not be named query parameters")
				}
				switch r.URL.Path {
				case "/zentao/api-getsessionid.json":
					pathSessions++
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": `{"sessionName":"sid","sessionID":"session-secret"}`})
				case "/zentao/user-login.json":
					loginCalls++
					if !strings.HasSuffix(r.Header.Get("Referer"), "/zentao/") || !strings.Contains(r.Header.Get("Referer"), r.Host) {
						t.Error("missing same-origin Referer required by 16.0 filterCSRF")
					}
					_ = r.ParseForm()
					cookie, _ := r.Cookie("sid")
					if r.Method != "POST" || r.PostForm.Get("account") != "sync" || r.PostForm.Get("password") != "secret-password" || cookie == nil || cookie.Value != "session-secret" {
						t.Error("missing safe login/session")
					}
					_, _ = w.Write([]byte("\ufeff" + `{"status":"success","user":{"account":"sync","token":"private-token"}}`))
				case "/zentao/bug-browse-17-0-all-0-id_asc-0-100-2.json":
					bugs++
					if cookie, _ := r.Cookie("sid"); cookie == nil {
						t.Error("lost authenticated session")
					}
					_, _ = w.Write([]byte(`{"status":"success","data":{"bugs":[{"id":"7","title":"test","status":"closed"}]}}`))
				case "/zentao/company-browse-inside-0-bydept-id_asc-0-100-1.json":
					users++
					_, _ = w.Write([]byte(`{"status":"success","data":"{\"users\":[{\"id\":\"1\",\"account\":\"sync\",\"realname\":\"同步用户\"}]}"}`))
				default:
					t.Errorf("unexpected route: %s", r.URL)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			connection := ResourceConnection{Name: "zt", Kind: "zentao", Endpoint: server.URL + "/zentao/", Username: "sync", Password: "secret-password", Config: map[string]string{}}
			z, err := newZentaoLegacy(context.Background(), &connection)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := z.bugs(context.Background(), 17, 2)
			if err != nil || len(rows) != 1 {
				t.Fatalf("bugs: %v %v", rows, err)
			}
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err = st.UpsertConnection(connection); err != nil {
				t.Fatal(err)
			}
			directory, err := st.zentaoUsers(context.Background(), "zt")
			if err != nil || len(directory.Users) != 1 {
				t.Fatalf("users: %+v %v", directory, err)
			}
			if err = testIntegrationConnection(context.Background(), &connection); err != nil {
				t.Fatalf("user-only connection: %v", err)
			}
			if getCalls != 3 || pathSessions != 3 || loginCalls != 3 || bugs != 1 || users != 1 {
				t.Fatalf("unexpected counts GET=%d PATH=%d login=%d bug=%d users=%d", getCalls, pathSessions, loginCalls, bugs, users)
			}
			if _, err = st.syncZentao(context.Background(), "zt", "", "local"); err == nil || !strings.Contains(err.Error(), "产品 ID") {
				t.Fatal("Bug sync must still require a product scope")
			}
		})
	}
}

func TestZentaoLoginFailureNeverRetriesOrLeaks(t *testing.T) {
	for _, response := range []string{
		`{"status":"failed","reason":"private upstream info secret-password"}`,
		`{"status":"success","user":{"account":"other"}}`,
		`{"status":"success"}`,
		`<html>secret-password session-secret</html>`,
	} {
		t.Run(response[:20], func(t *testing.T) {
			calls, logins := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/zentao/index.php" {
					t.Error("unexpected fallback")
				}
				if r.URL.Query().Get("f") == "getSessionID" {
					_, _ = w.Write([]byte(`{"status":"success","data":{"sessionName":"sid","sessionID":"session-secret"}}`))
				} else {
					logins++
					_, _ = w.Write([]byte(response))
				}
			}))
			defer server.Close()
			_, err := newZentaoLegacy(context.Background(), &ResourceConnection{Kind: "zentao", Endpoint: server.URL + "/zentao", Username: "sync", Password: "secret-password", Config: map[string]string{}})
			if err == nil {
				t.Fatal("accepted unauthenticated login")
			}
			if strings.Contains(err.Error(), "secret-password") || strings.Contains(err.Error(), "session-secret") || strings.Contains(err.Error(), "private upstream") {
				t.Fatal("leaked upstream data")
			}
			if calls != 2 || logins != 1 {
				t.Fatalf("login retried: %d/%d", calls, logins)
			}
		})
	}
}

func TestZentaoPathInfoRejectsUnknownOrUnsafeArguments(t *testing.T) {
	z := &zentaoLegacy{root: "http://example.test/zentao", pathInfo: true}
	if got := z.bugURL("7"); got != "http://example.test/zentao/bug-view-7.html" {
		t.Fatalf("bad PATHINFO Bug link: %s", got)
	}
	z.pathInfo = false
	if got := z.bugURL("7"); got != "http://example.test/zentao/index.php?m=bug&f=view&bugID=7" {
		t.Fatalf("bad GET Bug link: %s", got)
	}
	z.pathInfo = true
	for _, args := range []url.Values{{"pageID": {"1/../../"}}, {"pageID": {"1-2"}}, {"password": {"secret"}}, {"pageID": {""}}} {
		if _, err := z.requestURL("bug", "browse", args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := z.requestURL("user", "delete", nil); err == nil {
		t.Fatal("accepted unsupported method")
	}
}

func TestZentaoRequirementPathInfoPositionsAndGuards(t *testing.T) {
	z := &zentaoLegacy{root: "http://example.test/zentao", pathInfo: true}
	for _, tc := range []struct {
		module, method string
		args           url.Values
		suffix         string
	}{
		{"product", "browse", url.Values{"productID": {"13"}, "pageID": {"2"}, "orderBy": {"lastEditedDate_desc"}}, "/product-browse-13-all-all-0-story-lastEditedDate_desc-0-100-2.json"},
		{"story", "edit", url.Values{"storyID": {"7"}}, "/story-edit-7.json"},
	} {
		got, err := z.requestURL(tc.module, tc.method, tc.args)
		if err != nil || got != z.root+tc.suffix {
			t.Fatalf("%s/%s: %s %v", tc.module, tc.method, got, err)
		}
	}
	for _, args := range []url.Values{{"pageID": {"1/../../"}}, {"pageID": {"1-2"}}, {"password": {"secret"}}, {"storyType": {""}}} {
		if _, err := z.requestURL("product", "browse", args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := z.requestURL("story", "edit", url.Values{"storyID": {"7-8"}}); err == nil {
		t.Fatal("unsafe story ID accepted")
	}
	if _, err := z.requestURL("story", "delete", nil); err == nil {
		t.Fatal("unsupported story action accepted")
	}
}
