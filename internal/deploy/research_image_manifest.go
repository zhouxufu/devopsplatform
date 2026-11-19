package deploy

import (
	"encoding/json"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	nethtml "golang.org/x/net/html"
)

const researchImageManifestLimit = 100

type researchImageReference struct {
	FileID    int64    `json:"file_id"`
	Name      string   `json:"name,omitempty"`
	Locations []string `json:"locations"`
}

// References are discovery hints, never authority to fetch a file. The existing
// authenticated image route independently rechecks source product and ownership.
func researchImageManifest(v *ResearchRemote, root string) map[string]any {
	refs := map[string]*researchImageReference{}
	add := func(id, name, location string) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
			return
		}
		ref := refs[id]
		if ref == nil {
			ref = &researchImageReference{FileID: n, Locations: []string{}}
			refs[id] = ref
		}
		if name != "" {
			ref.Name = name
		}
		for _, existing := range ref.Locations {
			if existing == location {
				return
			}
		}
		ref.Locations = append(ref.Locations, location)
	}
	scan := func(body, location string) {
		base, err := url.Parse(strings.TrimRight(root, "/") + "/")
		if err != nil {
			return
		}
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
				if m := researchInlineImageRE.FindStringSubmatch(src); m != nil {
					add(m[1], "", location)
					continue
				}
				address, err := url.Parse(src)
				if err != nil || address.User != nil {
					continue
				}
				address = base.ResolveReference(address)
				if address.Scheme != base.Scheme || address.Host != base.Host || !strings.HasPrefix(address.Path, base.Path) {
					continue
				}
				name := strings.TrimPrefix(address.Path, base.Path)
				if m := researchReadImageRE.FindStringSubmatch(name); m != nil {
					add(m[1], "", location)
					continue
				}
				q := address.Query()
				if name == "index.php" && q.Get("m") == "file" && q.Get("f") == "read" {
					add(q.Get("fileID"), "", location)
				}
			}
		}
	}
	if v != nil {
		scan(researchValue(v.Bug, "steps"), "description")
		var actions any
		if json.Unmarshal(v.Actions, &actions) == nil {
			visit := func(a any) {
				if entry, ok := a.(map[string]any); ok {
					scan(researchValue(entry, "comment"), "comment")
				}
			}
			switch list := actions.(type) {
			case map[string]any:
				for _, a := range list {
					visit(a)
				}
			case []any:
				for _, a := range list {
					visit(a)
				}
			}
		}
		file := func(id string, a any) {
			entry, ok := a.(map[string]any)
			if !ok {
				return
			}
			if id == "" {
				id = researchValue(entry, "id")
			}
			name := researchValue(entry, "title")
			if name == "" {
				name = researchValue(entry, "name")
			}
			ext := strings.ToLower(strings.TrimPrefix(researchValue(entry, "extension"), "."))
			if ext == "" {
				ext = strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
			}
			switch ext {
			case "png", "jpg", "jpeg", "gif", "webp", "bmp", "avif":
				add(id, name, "attachment")
			}
		}
		switch files := v.Bug["files"].(type) {
		case map[string]any:
			for id, a := range files {
				file(id, a)
			}
		case []any:
			for _, a := range files {
				file("", a)
			}
		}
	}
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.ParseInt(ids[i], 10, 64)
		b, _ := strconv.ParseInt(ids[j], 10, 64)
		return a < b
	})
	truncated := len(ids) > researchImageManifestLimit
	if truncated {
		ids = ids[:researchImageManifestLimit]
	}
	items := make([]researchImageReference, 0, len(ids))
	for _, id := range ids {
		ref := refs[id]
		sort.Strings(ref.Locations)
		items = append(items, *ref)
	}
	return map[string]any{"items": items, "limit": researchImageManifestLimit, "truncated": truncated, "read_tool": "get_work_item_image", "verification": "候选图片引用；读取时重新校验来源权限与附件归属，不接收任意 URL。"}
}
