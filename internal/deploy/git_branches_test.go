package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitBranchParser(t *testing.T) {
	id := strings.Repeat("a", 40)
	r, err := parseGitBranches("ref: refs/heads/main\tHEAD\n" + id + "\tHEAD\n" + id + "\trefs/heads/main\n" + id + "\trefs/tags/v1\n" + id + "\trefs/heads/feature/menu\n" + id + "\trefs/heads/main\n")
	if err != nil || r.DefaultBranch != "main" || len(r.Branches) != 2 || r.Branches[0].Name != "feature/menu" {
		t.Fatalf("%#v %v", r, err)
	}
	var large strings.Builder
	for i := 0; i < 10001; i++ {
		large.WriteString(id + "\trefs/heads/b" + strings.Repeat("x", i%20) + string(rune(0x4e00+i)) + "\n")
	}
	if _, err := parseGitBranches(large.String()); err == nil {
		t.Fatal("missing branch limit")
	}
}

func TestGitDiscoveryRejectsUnsafeRepositories(t *testing.T) {
	for _, raw := range []string{"", "-oProxyCommand=bad", "file:///tmp/repo", "/tmp/repo", "ext::sh command", "https://user:token@example.test/repo.git", "https://git@example.test/repo.git", "ssh://git:secret@example.test/repo", "https://example.test/repo?token=secret", "git@example.test:path\ncommand"} {
		if validateGitDiscoveryURL(raw) == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://example.test/team/repo.git", "http://127.0.0.1:8000/repo.git", "ssh://git@example.test:2222/team/repo.git", "git@example.test:team/repo.git"} {
		if err := validateGitDiscoveryURL(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
}

func TestGitDiscoveryUsesSelectedCredentialAndDraftOverride(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertConnection(ResourceConnection{Name: "git-test", Kind: "git", Endpoint: "https://example.test", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertService(Service{Name: "app", GitURL: "https://example.test/repo.git", ArtifactType: "jar"}); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertServiceDelivery(ServiceDelivery{Service: "app", Runner: "local", GitConnection: "git-test"}); err != nil {
		t.Fatal(err)
	}
	u, c, err := s.gitDiscoverySource(GitBranchesRequest{Service: "app"})
	if err != nil || u != "https://example.test/repo.git" || c.Password != "secret" {
		t.Fatalf("%s %#v %v", u, c, err)
	}
	empty := ""
	public := "https://public.example.test/repo.git"
	_, c, err = s.gitDiscoverySource(GitBranchesRequest{Service: "app", GitURL: &public, Connection: &empty})
	if err != nil || c != nil {
		t.Fatalf("explicit no-credential override failed: %v", err)
	}
	for _, raw := range []string{"https://evil.example.test/repo.git", "http://example.test/repo.git", "https://example.test:444/repo.git", "git@example.test:repo.git"} {
		if _, _, err = s.gitDiscoverySource(GitBranchesRequest{Service: "app", GitURL: &raw}); err == nil {
			t.Errorf("sent credentials to %s", raw)
		}
	}
	if _, _, err = s.gitDiscoverySource(GitBranchesRequest{Service: "missing"}); err == nil {
		t.Fatal("accepted missing application")
	}
}

func TestGitDiscoveryHTTPSReadOnlyIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	bare := filepath.Join(dir, "repo.git")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	run("init", work)
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("-C", work, "add", "README")
	run("-C", work, "-c", "user.email=fixture@example.test", "-c", "user.name=fixture", "commit", "-m", "fixture")
	run("-C", work, "branch", "-M", "main")
	run("-C", work, "branch", "feature/menu")
	run("-C", work, "tag", "v1")
	run("clone", "--bare", work, bare)
	run("--git-dir="+bare, "update-server-info")
	before, err := os.ReadFile(filepath.Join(bare, "packed-refs"))
	if err != nil {
		t.Fatal(err)
	}
	fs := http.FileServer(http.Dir(dir))
	var authCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "builder" || p != "fixture-token" {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			w.WriteHeader(401)
			return
		}
		authCalls.Add(1)
		fs.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := discoverGitBranches(ctx, server.URL+"/repo.git", &ResourceConnection{Username: "builder", Password: "fixture-token"})
	if err != nil || len(result.Branches) != 2 || result.DefaultBranch != "main" || authCalls.Load() == 0 {
		t.Fatalf("%#v %v auth=%d", result, err, authCalls.Load())
	}
	after, err := os.ReadFile(filepath.Join(bare, "packed-refs"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("remote references modified", err)
	}
}

func TestGitDiscoveryTemporarySSHMaterial(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "material")
	code := `#!/bin/sh
test "$1" = "-c" || exit 1
test -x "$GIT_SSH" || exit 1
test -z "${GIT_SSH_COMMAND:-}" || exit 1
test -f "$DEVOPS_GIT_KEY" || exit 1
test -f "$DEVOPS_GIT_KNOWN" || exit 1
grep -q StrictHostKeyChecking=yes "$GIT_SSH" || exit 1
grep -q IdentitiesOnly=yes "$GIT_SSH" || exit 1
printf '%s' "$DEVOPS_GIT_KEY" > "$TEST_MATERIAL"
printf '%s\trefs/heads/main\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(code), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("TEST_MATERIAL", marker)
	t.Setenv("GIT_SSH_COMMAND", "echo unsafe")
	result, err := discoverGitBranches(context.Background(), "git@example.test:repo.git", &ResourceConnection{Username: "git", PrivateKey: "TEST PRIVATE KEY", Config: map[string]string{"auth_mode": "ssh", "known_hosts": "example.test ssh-ed25519 aaa"}})
	if err != nil || len(result.Branches) != 1 {
		t.Fatalf("%#v %v", result, err)
	}
	path, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(string(path)); !os.IsNotExist(err) {
		t.Fatal("temporary private key remains")
	}
}

func TestGitDiscoveryErrorsNeverExposeStderrSecrets(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho 'Authentication failed https://user:secret@example.test' >&2\nexit 128\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	_, err := discoverGitBranches(context.Background(), "https://example.test/repo", nil)
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "认证失败") {
		t.Fatalf("unsafe error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = discoverGitBranches(ctx, "https://example.test/repo", nil); err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("%v", err)
	}
}

func TestGitBranchAPIRejectsInvalidInput(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mux := http.NewServeMux()
	registerGitBranchRoutes(mux, s)
	for _, body := range []string{`{`, `{"git_url":"file:///tmp/repo"}`, `{"service":"deleted"}`, `{"git_url":"https://user:secret@example.test/repo.git"}`} {
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest("POST", "/api/git/branches", bytes.NewBufferString(body)))
		var response map[string]any
		if json.Unmarshal(r.Body.Bytes(), &response) != nil || response["error"] == nil || r.Code == 200 {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
	}
}
