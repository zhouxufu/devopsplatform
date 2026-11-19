package deploy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResearchImageManifestScopedDeduplicatedAndBounded(t *testing.T) {
	v := &ResearchRemote{Bug: map[string]any{"steps": `<img src="{42.png}"><img src="file-read-42.png"><img src="https://evil.test/file-read-99.png"><img src="/elsewhere/file-read-98.png"><a href="file-read-97.png">not an image</a><img src="index.php?m=file&amp;f=read&amp;fileID=43"><img src="file-read-0.png">`, "files": map[string]any{"42": map[string]any{"title": "截图.png", "extension": "png"}, "44": map[string]any{"title": "附件.jpg"}, "45": map[string]any{"title": "说明.pdf"}}}, Actions: json.RawMessage(`{"1":{"comment":"<img src=\"{42.png}\"><img src=\"file-read-46.gif\">"}}`)}
	m := researchImageManifest(v, "https://source.test/zentao")
	items := m["items"].([]researchImageReference)
	if len(items) != 4 || items[0].FileID != 42 || items[0].Name != "截图.png" || strings.Join(items[0].Locations, ",") != "attachment,comment,description" || items[1].FileID != 43 || items[2].FileID != 44 || items[3].FileID != 46 {
		t.Fatalf("manifest=%+v", m)
	}
	for _, item := range items {
		if !researchImageReferenced(v, "https://source.test/zentao", fmt.Sprint(item.FileID)) && !researchFileAllowed(v, fmt.Sprint(item.FileID)) {
			t.Fatal("undiscoverable reference", item)
		}
	}
	var body strings.Builder
	for i := 1; i <= 105; i++ {
		fmt.Fprintf(&body, `<img src="{%d.png}">`, i)
	}
	v.Bug["steps"] = body.String()
	m = researchImageManifest(v, "https://source.test/zentao")
	items = m["items"].([]researchImageReference)
	if len(items) != 100 || m["truncated"] != true || items[99].FileID != 100 {
		t.Fatal("manifest limit", m)
	}
	v = &ResearchRemote{Bug: map[string]any{}, Actions: json.RawMessage(`[{"comment":"<img src=\"{7.png}\">"}]`)}
	if got := researchImageManifest(v, "https://source.test/zentao")["items"].([]researchImageReference); len(got) != 1 || got[0].FileID != 7 {
		t.Fatal(got)
	}
	if len(researchImageManifest(nil, "")["items"].([]researchImageReference)) != 0 {
		t.Fatal("nil remote")
	}
}

func TestResearchDetailExposesImageManifestWithoutFetchingImages(t *testing.T) {
	s, item, writes, mu := researchFixture(t, func(method string, bug map[string]any) { bug["steps"] = `<p>复现</p><img src="{42.png}">` })
	mux := http.NewServeMux()
	registerResearchRoutes(mux, s, nil, nil)
	r := httptest.NewRequest("GET", fmt.Sprintf("/api/research/work-items/%d", item.ID), nil)
	r.Header.Set("X-User", "platform-alice")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var result struct {
		Images struct {
			Items    []researchImageReference `json:"items"`
			ReadTool string                   `json:"read_tool"`
		} `json:"images"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Images.Items) != 1 || result.Images.ReadTool != "get_work_item_image" {
		t.Fatal(w.Code, w.Body.String())
	}
	mu.Lock()
	n := *writes
	mu.Unlock()
	if n != 0 {
		t.Fatal("discovery wrote source", n)
	}
	// An editable reference alone cannot grant access without native ownership.
	r = httptest.NewRequest("GET", fmt.Sprintf("/api/research/work-items/%d/images/42", item.ID), nil)
	r.Header.Set("X-User", "platform-alice")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("reference bypassed native ownership", w.Code, w.Body.String())
	}
}
