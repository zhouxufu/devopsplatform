package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZentao16UserDirectoryPaginationAndSanitization(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mode := "success"
	pages := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Query().Get("f") {
		case "getSessionID":
			data = map[string]string{"sessionName": "zentaosid", "sessionID": "secret-session"}
		case "login":
			_ = r.ParseForm()
			cookie, _ := r.Cookie("zentaosid")
			if r.Method != "POST" || r.PostForm.Get("password") != "sync-secret" || cookie == nil {
				t.Error("unsafe login")
			}
			data = map[string]any{"user": map[string]string{"account": "sync"}}
		case "browse":
			pages++
			if r.URL.Query().Get("m") != "company" || r.URL.Query().Get("browseType") != "inside" || r.URL.Query().Get("recPerPage") != "100" {
				t.Error("not the verified user directory endpoint")
			}
			if mode == "denied" {
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "fail"})
				return
			}
			items := []map[string]any{}
			if r.URL.Query().Get("pageID") == "1" || mode == "repeated" {
				for i := 1; i <= 100; i++ {
					items = append(items, map[string]any{"id": i, "account": fmt.Sprintf("user%d", i), "realname": "张三", "password": "upstream-hash", "role": "admin", "email": "private"})
				}
			} else {
				items = append(items, map[string]any{"id": "101", "account": "last", "realname": "最后一人", "deleted": "0"})
			}
			data = map[string]any{"users": items}
		default:
			t.Errorf("unexpected endpoint %s", r.URL)
		}
		raw, _ := json.Marshal(data)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
	}))
	defer remote.Close()
	if err = st.UpsertConnection(ResourceConnection{Name: "zentao", Kind: "zentao", Endpoint: remote.URL, Username: "sync", Password: "sync-secret", Config: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	d, err := st.zentaoUsers(context.Background(), "zentao")
	if err != nil || len(d.Users) != 101 || pages != 2 {
		t.Fatalf("pagination: %+v %v", d, err)
	}
	raw, _ := json.Marshal(d)
	for _, secret := range []string{"upstream-hash", "sync-secret", "secret-session", "email", "role"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	mode = "repeated"
	if _, err = st.zentaoUsers(context.Background(), "zentao"); err == nil {
		t.Fatal("accepted repeated pagination")
	}
	mode = "denied"
	if _, err = st.zentaoUsers(context.Background(), "zentao"); err == nil || !strings.Contains(err.Error(), "权限") {
		t.Fatal("permission error not actionable")
	}
}
