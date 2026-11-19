package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	nethtml "golang.org/x/net/html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func researchFileAllowed(v *ResearchRemote, id string) bool {
	if v == nil {
		return false
	}
	// Native attachment metadata is authoritative. Text can contain arbitrary
	// file IDs and must never grant access to another Bug's attachment.
	if files, ok := v.Bug["files"].(map[string]any); ok {
		if files[id] != nil {
			return true
		}
	}
	if files, ok := v.Bug["files"].([]any); ok {
		for _, file := range files {
			if record, ok := file.(map[string]any); ok && fmt.Sprint(record["id"]) == id {
				return true
			}
		}
	}
	return false
}

// ZenTao excludes editor images from bug.files. Verify their native file
// ownership instead of trusting IDs found in editable descriptions/comments.
func (z *zentaoLegacy) researchEditorFileAllowed(ctx context.Context, bugID, fileID string, linked ...func(string, string) bool) bool {
	var file map[string]any
	if z.call(ctx, "api", "getModel", url.Values{"moduleName": {"file"}, "methodName": {"getById"}, "params": {"fileID=" + fileID}}, nil, &file) != nil {
		return false
	}
	kind := researchValue(file, "objectType")
	if researchValue(file, "id") != fileID || (kind != "bug" && kind != "story") || researchValue(file, "deleted") == "1" {
		return false
	}
	owner := researchValue(file, "objectID")
	if kind == "bug" && owner == bugID {
		return true
	}
	id, err := strconv.ParseInt(owner, 10, 64)
	return err == nil && id > 0 && len(linked) == 1 && linked[0](kind, owner)
}

// Copied editor images require both an actual image reference in the current
// Bug and read access to their original Bug/story; text IDs alone grant no access.
var researchInlineImageRE = regexp.MustCompile(`^\{([1-9][0-9]*)(?:\.(?i:png|jpe?g|gif|webp|bmp|avif))?\}$`)
var researchReadImageRE = regexp.MustCompile(`^file-read-([1-9][0-9]*)\.(?i:html|png|jpe?g|gif|webp|bmp|avif)$`)

func researchImageReferenced(v *ResearchRemote, root, file string) bool {
	bodies := []string{researchValue(v.Bug, "steps")}
	var actions any
	if json.Unmarshal(v.Actions, &actions) == nil {
		appendAction := func(a any) {
			if entry, ok := a.(map[string]any); ok {
				bodies = append(bodies, researchValue(entry, "comment"))
			}
		}
		switch list := actions.(type) {
		case map[string]any:
			for _, a := range list {
				appendAction(a)
			}
		case []any:
			for _, a := range list {
				appendAction(a)
			}
		}
	}
	base, _ := url.Parse(root + "/")
	for _, body := range bodies {
		tokens := nethtml.NewTokenizer(strings.NewReader(body))
		for {
			kind := tokens.Next()
			if kind == nethtml.ErrorToken {
				break
			}
			if kind != nethtml.StartTagToken && kind != nethtml.SelfClosingTagToken {
				continue
			}
			token := tokens.Token()
			if token.Data != "img" {
				continue
			}
			for _, attr := range token.Attr {
				if attr.Key != "src" {
					continue
				}
				src := strings.TrimSpace(attr.Val)
				if match := researchInlineImageRE.FindStringSubmatch(src); match != nil && match[1] == file {
					return true
				}
				address, err := url.Parse(src)
				if err != nil || address.User != nil {
					continue
				}
				address = base.ResolveReference(address)
				if address.Scheme != base.Scheme || address.Host != base.Host {
					continue
				}
				if !strings.HasPrefix(address.Path, base.Path) {
					continue
				}
				name := strings.TrimPrefix(address.Path, base.Path)
				if match := researchReadImageRE.FindStringSubmatch(name); match != nil && match[1] == file {
					return true
				}
				query := address.Query()
				if name == "index.php" && query.Get("m") == "file" && query.Get("f") == "read" && query.Get("fileID") == file {
					return true
				}
			}
		}
	}
	return false
}

// This is an authenticated, Bug-scoped read proxy, not an arbitrary URL proxy.
func registerResearchImageRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /api/research/work-items/{id}/images/{file}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		file := r.PathValue("file")
		fid, err := strconv.ParseInt(file, 10, 64)
		if err != nil || fid <= 0 {
			deliveryWriteError(w, 400, "附件编号无效")
			return
		}
		item := s.GetWorkItem(id)
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		c, err := s.researchConnection(item)
		if err != nil {
			deliveryWriteError(w, 404, err)
			return
		}
		z, c, _, err := s.liveReadClient(ctx, c.Name, deliveryActor(r), false)
		if err != nil {
			deliveryWriteError(w, 403, err)
			return
		}
		remote, err := z.researchRead(ctx, item.ExternalID, "view")
		if err == nil {
			product, _ := strconv.Atoi(researchValue(remote.Bug, "product"))
			if !liveProductAllowed(c, product) {
				err = fmt.Errorf("附件不在连接允许的产品范围")
			}
		}
		if err != nil || (!researchFileAllowed(remote, file) && !z.researchEditorFileAllowed(ctx, item.ExternalID, file, func(kind, owner string) bool {
			if !researchImageReferenced(remote, z.root, file) {
				return false
			}
			var original map[string]any
			if kind == "bug" {
				record, e := z.researchRead(ctx, owner, "view")
				if e != nil {
					return false
				}
				original = record.Bug
			} else {
				var record struct {
					Story map[string]any `json:"story"`
				}
				if z.call(ctx, "story", "view", url.Values{"storyID": {owner}}, nil, &record) != nil || researchValue(record.Story, "id") != owner {
					return false
				}
				original = record.Story
			}
			product, _ := strconv.Atoi(researchValue(original, "product"))
			return liveProductAllowed(c, product)
		})) {
			deliveryWriteError(w, 403, "附件不属于可访问的当前 Bug")
			return
		}
		address := z.root + "/index.php?" + url.Values{"m": {"file"}, "f": {"read"}, "fileID": {file}}.Encode()
		if z.pathInfo {
			address = z.root + "/file-read-" + file + ".html"
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
		req.Header.Set("Referer", z.bugURL(item.ExternalID))
		resp, err := z.client.Do(req)
		if err != nil {
			deliveryWriteError(w, 502, "附件读取失败")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			deliveryWriteError(w, 502, fmt.Sprintf("附件读取返回 %d", resp.StatusCode))
			return
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxWorkImage+1))
		if err != nil || len(data) > maxWorkImage {
			deliveryWriteError(w, 502, "图片过大或读取失败")
			return
		}
		mime := http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			deliveryWriteError(w, 502, "来源返回的不是图片，请检查权限或附件状态")
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(data)
	})
}
