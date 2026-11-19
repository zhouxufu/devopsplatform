package devtools

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkLoopBackgroundFreshKeyAndUser(t *testing.T) {
	store, _ := OpenTokenStore(t.TempDir())
	key, _, e := store.Create(CreateTokenRequest{Name: "worker", Owner: "alice", Scopes: workLoopScopes, Applications: []string{"*"}, Environments: []string{"test"}})
	if e != nil {
		t.Fatal(e)
	}
	var writes, blocks atomic.Int32
	authority := "mcp"
	id := strings.Repeat("a", 32)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/advance") {
			writes.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/blocked") {
			blocks.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "owner": "alice", "revision": 1, "state": "running", "authority": authority, "authority_key_id": key.ID, "grant": map[string]any{"spec": map[string]any{"environment": "test", "applications": []string{"orders"}}}})
	}))
	defer up.Close()
	svc := NewToolService(Upstreams{Deploy: up.URL})
	h := Routes(store, svc)
	call := func(directory string) int {
		r := httptest.NewRequest("POST", "/internal/work-loop-advance", bytes.NewBufferString(`{"task_id":"`+id+`","owner":"alice"}`))
		r.Header.Set("X-MCP-Users", directory)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	enabled := `[{"username":"alice","role":"member","enabled":true}]`
	if code := call(enabled); code != 200 || writes.Load() != 1 {
		t.Fatal(code, writes.Load())
	}
	if code := call(`[{"username":"alice","role":"member","enabled":false}]`); code != 403 || writes.Load() != 1 {
		t.Fatal("disabled user dispatched")
	}
	revision := key.Revision
	_, e = store.maintainToken(key.ID, "alice", false, &UpdateTokenRequest{Revision: &revision, Name: key.Name, Scopes: workLoopScopes, Applications: []string{"other-app"}, Environments: []string{"test"}}, false)
	if e != nil {
		t.Fatal(e)
	}
	if code := call(enabled); code != 403 || writes.Load() != 1 || blocks.Load() != 1 {
		t.Fatal("narrowed Key scope dispatched", code, writes.Load(), blocks.Load())
	}
	if e = store.Revoke(key.ID); e != nil {
		t.Fatal(e)
	}
	if code := call(enabled); code != 403 || writes.Load() != 1 || blocks.Load() != 2 {
		t.Fatal("revoked Key dispatched", code, writes.Load(), blocks.Load())
	}
	authority = "page"
	if code := call(enabled); code != 200 || writes.Load() != 2 {
		t.Fatal("page authorization requires Key", code)
	}
	authority = ""
	if code := call(enabled); code != 403 || writes.Load() != 2 {
		t.Fatal("legacy task silently elevated")
	}
}
