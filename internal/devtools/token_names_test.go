package devtools

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestTokenListDisplaysBoundAccountNameAndKeepsOwnerIsolation(t *testing.T) {
	store, e := OpenTokenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, owner := range []string{"alice", "bob"} {
		if _, _, e = store.Create(CreateTokenRequest{Owner: owner, Name: "same label", Scopes: []string{"logs:read"}, Applications: []string{"*"}, Environments: []string{"test"}}); e != nil {
			t.Fatal(e)
		}
	}
	h := Routes(store, NewToolService(Upstreams{}))
	r := httptest.NewRequest("GET", "/api/tokens", nil)
	r.Header.Set("X-User", "alice")
	r.Header.Set("X-Role", "member")
	r.Header.Set("X-User-Display-Names", `{"alice":"艾丽丝","bob":"Bob"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var rows []map[string]any
	json.Unmarshal(w.Body.Bytes(), &rows)
	if w.Code != 200 || len(rows) != 1 || rows[0]["owner"] != "alice" || rows[0]["owner_name"] != "艾丽丝" || rows[0]["hash"] != nil {
		t.Fatal(w.Code, w.Body.String())
	}
}
