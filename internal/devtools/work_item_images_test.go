package devtools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func workImagePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestWorkItemImageMCPWireAndIdentity(t *testing.T) {
	data := workImagePNG(t)
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/research/work-items/7/images/42" || r.URL.RawQuery != "" || r.Header.Get("X-User") != "alice" || r.Header.Get("X-Role") != "member" {
			t.Error("wrong request identity", r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
	defer up.Close()
	store, err := OpenTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := store.Create(CreateTokenRequest{Owner: "alice", Name: "images", Scopes: []string{"workitems:read"}, Applications: []string{"*"}, Environments: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewToolService(Upstreams{Deploy: up.URL})
	h := NewMCPHandler(store, svc)
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_work_item_image","arguments":{"work_item_id":7,"file_id":42}}}`))
	r.Header.Set("Authorization", "Bearer "+raw)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("X-MCP-Users", `[{"username":"alice","enabled":true,"role":"member"}]`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got struct {
		Result struct {
			IsError bool             `json:"isError"`
			Content []map[string]any `json:"content"`
		} `json:"result"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if w.Code != 200 || got.Result.IsError || len(got.Result.Content) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	img := got.Result.Content[1]
	decoded, err := base64.StdEncoding.DecodeString(img["data"].(string))
	if err != nil || img["type"] != "image" || img["mimeType"] != "image/png" || !bytes.Equal(decoded, data) || calls.Load() != 1 {
		t.Fatal("not a native image", img["type"], err)
	}
	// Ordinary source JSON cannot impersonate MCP image blocks.
	v := mapValue(mcpSuccessResult(map[string]any{"type": "image", "data": "untrusted"}))
	if v["content"].([]map[string]any)[0]["type"] != "text" {
		t.Fatal("content injection")
	}
}

func TestWorkItemImagePermissionsAndInputsBeforeFetch(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write(workImagePNG(t)) }))
	defer up.Close()
	store, _ := OpenTokenStore(t.TempDir())
	svc := NewToolService(Upstreams{Deploy: up.URL})
	tok := workflowToken(t, store)
	for _, change := range []func(*AccessToken){func(t *AccessToken) { t.Scopes = nil }, func(t *AccessToken) { t.Applications = []string{"orders"} }, func(t *AccessToken) { t.verified = false }, func(t *AccessToken) { t.Owner = "local" }} {
		other := *tok
		change(&other)
		if _, _, _, err := svc.Call(context.Background(), &other, "get_work_item_image", map[string]any{"work_item_id": 7, "file_id": 42}); err == nil {
			t.Fatal("permission bypass")
		}
	}
	for _, bad := range []any{0, -1, 1.5, "42", "../42", "https://evil.test/42"} {
		if _, _, _, err := svc.Call(context.Background(), tok, "get_work_item_image", map[string]any{"work_item_id": 7, "file_id": bad}); err == nil {
			t.Fatal("bad image ID accepted", bad)
		}
	}
	if _, _, _, err := svc.Call(context.Background(), tok, "get_work_item_image", map[string]any{"work_item_id": 7, "file_id": 42, "url": "https://evil.test/"}); err == nil {
		t.Fatal("arbitrary URL accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized fetch", calls.Load())
	}
}

func TestWorkItemImageUpstreamErrorsAndBounds(t *testing.T) {
	data := workImagePNG(t)
	huge := append([]byte(nil), data...)
	binary.BigEndian.PutUint32(huge[16:20], 100000)
	binary.BigEndian.PutUint32(huge[20:24], 100000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	for _, tc := range []struct {
		name     string
		status   int
		body     []byte
		redirect bool
	}{
		{"login HTML", 200, []byte("<html>login</html>"), false}, {"missing", 404, []byte("missing"), false}, {"permission", 403, []byte("denied"), false}, {"oversize", 200, make([]byte, maxMCPWorkImage+1), false}, {"huge dimensions", 200, huge, false}, {"redirect", 302, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var followed atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1); w.Write(data) }))
			defer other.Close()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.redirect {
					w.Header().Set("Location", other.URL)
				}
				w.WriteHeader(tc.status)
				w.Write(tc.body)
			}))
			defer up.Close()
			store, _ := OpenTokenStore(t.TempDir())
			tok := workflowToken(t, store)
			svc := NewToolService(Upstreams{Deploy: up.URL})
			if _, _, _, err := svc.Call(context.Background(), tok, "get_work_item_image", map[string]any{"work_item_id": 7, "file_id": 42}); err == nil {
				t.Fatal("bad upstream accepted")
			}
			if followed.Load() != 0 {
				t.Fatal("identity redirect followed")
			}
		})
	}
}

func TestWorkItemImageSupportedFormats(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			var b bytes.Buffer
			var err error
			switch format {
			case "png":
				err = png.Encode(&b, img)
			case "jpeg":
				err = jpeg.Encode(&b, img, nil)
			case "gif":
				err = gif.Encode(&b, img, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := decodeWorkItemImage(b.Bytes())
			if err != nil || result.Width != 2 || result.Height != 2 {
				t.Fatal(result, err)
			}
		})
	}
}
