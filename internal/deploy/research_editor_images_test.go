package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestResearchEditorImageVerifiesNativeOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, objectType, objectID, deleted string
		allowed                             bool
	}{{"editor image", "bug", "7", "0", true}, {"foreign Bug", "bug", "8", "0", false}, {"other object", "story", "7", "0", false}, {"deleted", "bug", "7", "1", false}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Query().Get("m") != "api" || r.URL.Query().Get("f") != "getModel" || r.URL.Query().Get("params") != "fileID=42" {
					t.Error("unexpected metadata request", r.URL)
				}
				raw, _ := json.Marshal(map[string]string{"id": "42", "objectType": tc.objectType, "objectID": tc.objectID, "deleted": tc.deleted, "extra": "editor"})
				json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": string(raw)})
			}))
			defer server.Close()
			z := &zentaoLegacy{root: server.URL, client: server.Client()}
			if !tc.allowed && tc.deleted == "0" {
				called := false
				allow := z.researchEditorFileAllowed(context.Background(), "7", "42", func(kind, owner string) bool { called = true; return kind == tc.objectType && owner == tc.objectID })
				if !allow || !called {
					t.Fatal("accessible linked image was blocked")
				}
				if z.researchEditorFileAllowed(context.Background(), "7", "42", func(string, string) bool { return false }) {
					t.Fatal("inaccessible original record was accepted")
				}
			}
			if got := z.researchEditorFileAllowed(context.Background(), "7", "42"); got != tc.allowed {
				t.Fatalf("ownership accepted=%v", got)
			}
		})
	}
}
func TestZentaoFileMetadataRoutesAreNarrowlyAllowed(t *testing.T) {
	for _, pathInfo := range []bool{false, true} {
		z := &zentaoLegacy{root: "http://example.test/zentao", pathInfo: pathInfo}
		args := url.Values{"moduleName": {"file"}, "methodName": {"getById"}, "params": {"fileID=42"}}
		address, err := z.requestURL("api", "getModel", args)
		if err != nil || !strings.Contains(address, "get") {
			t.Fatal(address, err)
		}
		if pathInfo && !strings.Contains(address, "api-getmodel-file-getbyid-") {
			t.Fatal(address)
		}
		for _, bad := range []string{"fileID=42&objectID=7", "fileID=../42", "fileID=0", "fileID=042"} {
			args.Set("params", bad)
			if _, err = z.requestURL("api", "getModel", args); err == nil {
				t.Fatal("unsafe metadata parameters accepted", bad)
			}
		}
		args.Set("params", "fileID=42")
		args.Set("methodName", "delete")
		if _, err = z.requestURL("api", "getModel", args); err == nil {
			t.Fatal("file write method accepted")
		}
	}
}

func TestResearchInlineImageReferenceIsScoped(t *testing.T) {
	for _, tc := range []struct {
		body    string
		allowed bool
	}{
		{`<img src="{42.png}">`, true},
		{`<img src="file-read-42.png">`, true},
		{`<img src="http://source.test/zentao/index.php?m=file&amp;f=read&amp;fileID=42">`, true},
		{`<img src="http://evil.test/file-read-42.png">`, false},
		{`<a href="file-read-42.png">42</a>`, false},
		{`<img src="{420.png}">`, false},
	} {
		v := &ResearchRemote{Bug: map[string]any{"steps": tc.body}}
		if got := researchImageReferenced(v, "http://source.test/zentao", "42"); got != tc.allowed {
			t.Fatal(tc.body, got)
		}
		v = &ResearchRemote{Bug: map[string]any{}, Actions: json.RawMessage(`{"1":{"comment":` + strconv.Quote(tc.body) + `}}`)}
		if got := researchImageReferenced(v, "http://source.test/zentao", "42"); got != tc.allowed {
			t.Fatal("comment", tc.body, got)
		}
	}
}
