package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type sessionProbe struct {
	mu                    sync.Mutex
	logins, reads, writes int
	failure               string
	cookies               map[string]string
}

func sessionFixture(t *testing.T) (*Store, *WorkItem, *sessionProbe) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	probe := &sessionProbe{cookies: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		reply := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			raw, _ := json.Marshal(v)
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
		}
		switch r.URL.Query().Get("f") {
		case "getSessionID":
			reply(map[string]string{"sessionName": "zentaosid", "sessionID": "anonymous"})
			return
		case "login":
			r.ParseForm()
			account := r.PostForm.Get("account")
			probe.logins++
			if r.PostForm.Get("password") != account+"-secret" {
				json.NewEncoder(w).Encode(map[string]string{"status": "fail"})
				return
			}
			cookie := fmt.Sprintf("%s-%d", account, probe.logins)
			probe.cookies[cookie] = account
			http.SetCookie(w, &http.Cookie{Name: "zentaosid", Value: cookie, Path: "/"})
			reply(map[string]any{"user": map[string]string{"account": account}})
			return
		}
		cookie, _ := r.Cookie("zentaosid")
		account := ""
		if cookie != nil {
			account = probe.cookies[cookie.Value]
		}
		probe.reads++
		if r.Method != http.MethodGet {
			probe.writes++
		}
		if account == "" {
			w.WriteHeader(401)
			return
		}
		if account == "alice" {
			switch probe.failure {
			case "expired":
				w.WriteHeader(401)
				return
			case "redirect":
				http.Redirect(w, r, "/zentao/user-login.html", 302)
				return
			case "html":
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<form action="user-login.html" id="loginForm"></form>`)
				return
			case "locate":
				reply(map[string]string{"locate": "/zentao/index.php?m=user&f=login"})
				return
			case "permission":
				json.NewEncoder(w).Encode(map[string]string{"status": "fail", "message": "denied"})
				return
			case "network":
				w.WriteHeader(503)
				return
			}
		}
		if r.URL.Query().Get("m") == "company" {
			reply(map[string]any{"users": []any{map[string]any{"id": 1, "account": "alice", "realname": "Alice", "deleted": 0}, map[string]any{"id": 2, "account": "bob", "realname": "Bob", "deleted": 0}}})
			return
		}
		reply(map[string]any{"bug": map[string]string{"id": "7", "title": "Bug", "status": "active", "assignedTo": account}})
	}))
	t.Cleanup(server.Close)
	err = s.UpsertConnection(ResourceConnection{Name: "issues", Kind: "zentao", Endpoint: server.URL + "/zentao", Username: "sync", Password: "sync-secret", Config: map[string]string{"product_ids": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	item := &WorkItem{Source: "zentao:issues", ExternalID: "7", Kind: "bug", Title: "Bug", Owner: "sync"}
	if err = s.CreateWorkItem(item); err != nil {
		t.Fatal(err)
	}
	return s, item, probe
}

func TestZentaoSessionsReuseAndIsolateActors(t *testing.T) {
	s, item, p := sessionFixture(t)
	ctx := context.Background()
	if err := s.AuthorizeResearch(ctx, "issues", "platform-alice", ResearchCredential{Account: "alice", Password: "alice-secret"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			z, a, err := s.researchClient(ctx, item, "platform-alice")
			if err != nil {
				t.Error(err)
				return
			}
			remote, err := z.researchRead(ctx, "7", "view")
			if err != nil || a != "alice" || remote.Bug["assignedTo"] != "alice" {
				t.Error("wrong actor", err)
			}
		}()
	}
	wg.Wait()
	p.mu.Lock()
	count := p.logins
	p.mu.Unlock()
	if count != 1 {
		t.Fatal("parallel clients relogged", count)
	}
	if _, _, err := s.researchClient(ctx, item, "unverified"); err == nil {
		t.Fatal("public identity used for personal read")
	}
	if err := s.AuthorizeResearch(ctx, "issues", "platform-bob", ResearchCredential{Account: "bob", Password: "bob-secret"}); err != nil {
		t.Fatal(err)
	}
	bob, _, _ := s.researchClient(ctx, item, "platform-bob")
	remote, err := bob.researchRead(ctx, "7", "view")
	if err != nil || remote.Bug["assignedTo"] != "bob" {
		t.Fatal("cross-user cookie", err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.zentaoUsers(ctx, "issues"); err != nil {
			t.Fatal(err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.logins != 3 || p.writes != 0 {
		t.Fatal("public session not reused or business write occurred", p.logins, p.writes)
	}
}

func TestZentaoSessionsExpireWithoutReplayAndReverify(t *testing.T) {
	for _, failure := range []string{"expired", "redirect", "html", "locate"} {
		t.Run(failure, func(t *testing.T) {
			s, item, p := sessionFixture(t)
			ctx := context.Background()
			if err := s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "alice", Password: "alice-secret"}); err != nil {
				t.Fatal(err)
			}
			z, _, _ := s.researchClient(ctx, item, "alice")
			p.mu.Lock()
			p.failure = failure
			p.mu.Unlock()
			_, err := z.researchRead(ctx, "7", "view")
			var auth *zentaoAuthenticationError
			if !errors.As(err, &auth) {
				t.Fatal("missing auth expiry", err)
			}
			_, status := s.researchAuthStatus("issues", "alice")
			if status != "needs_verification" {
				t.Fatal(status)
			}
			if _, _, err = s.researchClient(ctx, item, "alice"); !errors.As(err, &auth) {
				t.Fatal("expired session auto-retried", err)
			}
			p.mu.Lock()
			if p.logins != 1 || p.reads != 1 || p.writes != 0 {
				t.Fatal("unexpected replay", p.logins, p.reads, p.writes)
			}
			p.failure = ""
			p.mu.Unlock()
			if err = s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "alice", Password: "alice-secret"}); err != nil {
				t.Fatal(err)
			}
			next, _, err := s.researchClient(ctx, item, "alice")
			if err != nil || next == z {
				t.Fatal("old session retained", err)
			}
			if _, err = next.researchRead(ctx, "7", "view"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestZentaoSessionsPermissionFailureIsNotCredentialFailure(t *testing.T) {
	s, item, p := sessionFixture(t)
	ctx := context.Background()
	s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "alice", Password: "alice-secret"})
	for _, failure := range []string{"permission", "network"} {
		p.mu.Lock()
		p.failure = failure
		p.mu.Unlock()
		z, _, err := s.researchClient(ctx, item, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = z.researchRead(ctx, "7", "view"); err == nil {
			t.Fatal("failure ignored")
		}
		_, status := s.researchAuthStatus("issues", "alice")
		if status != "connected" {
			t.Fatal("transient/permission failure demands password", status)
		}
	}
}

func TestZentaoSessionsSavedPasswordTTLRevocationAndEndpointChange(t *testing.T) {
	s, item, p := sessionFixture(t)
	ctx := context.Background()
	if err := s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "alice"}); err == nil {
		t.Fatal("blank fell back to public password")
	}
	s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "alice", Password: "alice-secret"})
	if err := s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "bob"}); err == nil {
		t.Fatal("blank password changed actor")
	}
	if err := s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "alice"}); err != nil {
		t.Fatal(err)
	}
	entry := s.zentaoSession(zentaoSessionKey{"alice", "issues"})
	entry.mu.Lock()
	entry.lastUsed = time.Now().Add(-zentaoSessionIdle - time.Second)
	entry.mu.Unlock()
	z, _, err := s.researchClient(ctx, item, "alice")
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	count := p.logins
	p.mu.Unlock()
	if count != 3 {
		t.Fatal("idle session not refreshed", count)
	}
	s.db.Exec(`DELETE FROM research_credentials WHERE actor=? AND connection_name=?`, "alice", "issues")
	s.forgetZentaoSession("alice", "issues")
	if _, _, err = s.researchClient(ctx, item, "alice"); err == nil {
		t.Fatal("removed authorization retained")
	}
	if _, err = z.researchRead(ctx, "7", "view"); err == nil {
		t.Fatal("revoked cached handle retained")
	}
	s.AuthorizeResearch(ctx, "issues", "alice", ResearchCredential{Account: "alice", Password: "alice-secret"})
	c := s.GetConnection("issues")
	c.Endpoint += "/new-install"
	if err = s.UpsertConnection(*c); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.researchClient(ctx, item, "alice"); err == nil {
		t.Fatal("old credentials carried to different endpoint")
	}
}

func TestZentaoSessionsHintDoesNotGrantAuthorization(t *testing.T) {
	s, item, p := sessionFixture(t)
	ctx := context.Background()
	c := s.GetConnection("issues")
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("X-User", "platform-alice")
	raw, _ := json.Marshal([]map[string]string{{"source": researchSourceIdentity(c.Endpoint), "external_id": "1", "username": "platform-alice"}, {"source": researchSourceIdentity(c.Endpoint), "external_id": "2", "username": "other"}})
	request.Header.Set("X-Research-Identities", string(raw))
	hint, err := s.researchAccountHint(ctx, request, "issues")
	if err != nil || hint != "alice" {
		t.Fatal("linked account not prefilled", hint, err)
	}
	if _, _, err = s.researchClient(ctx, item, "platform-alice"); err == nil {
		t.Fatal("identity mapping silently authorized")
	}
	request.Header.Set("X-User", "unlinked")
	hint, err = s.researchAccountHint(ctx, request, "issues")
	if err != nil || hint != "" {
		t.Fatal("foreign mapping exposed")
	}
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	request = httptest.NewRequest("GET", "/api/research/overview", nil)
	request.Header.Set("X-User", "platform-alice")
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, request)
	if strings.Contains(out.Body.String(), "secret") || strings.Contains(out.Body.String(), "password") {
		t.Fatal("credential exposed")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writes != 0 {
		t.Fatal("hint wrote source")
	}
}
