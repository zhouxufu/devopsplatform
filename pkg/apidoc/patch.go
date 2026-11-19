package apidoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Patch is deliberately limited to atomic, deterministic JSON Patch operations.
// Values remain RawMessage so examples containing 64-bit integers survive.
type Patch struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

func ApplyPatch(raw []byte, patches []Patch) ([]byte, error) {
	if len(patches) == 0 || len(patches) > 200 {
		return nil, fmt.Errorf("一次需要 1–200 项局部修改")
	}
	if len(raw) > MaxDocument {
		return nil, fmt.Errorf("文档超过 8 MiB")
	}
	var root any
	decode := func(b []byte, v *any) error {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		return d.Decode(v)
	}
	if e := decode(raw, &root); e != nil {
		return nil, e
	}
	for i, p := range patches {
		if !strings.HasPrefix(p.Path, "/") || len(p.Path) > 8192 {
			return nil, fmt.Errorf("第 %d 项需要有效的 JSON Pointer", i+1)
		}
		parts := strings.Split(p.Path[1:], "/")
		for j, v := range parts {
			for k := 0; k < len(v); k++ {
				if v[k] == '~' {
					if k+1 >= len(v) || (v[k+1] != '0' && v[k+1] != '1') {
						return nil, fmt.Errorf("无效的 JSON Pointer 转义")
					}
					k++
				}
			}
			parts[j] = strings.ReplaceAll(strings.ReplaceAll(v, "~1", "/"), "~0", "~")
		}
		var value any
		if p.Op != "remove" {
			if len(p.Value) == 0 || decode(p.Value, &value) != nil {
				return nil, fmt.Errorf("第 %d 项缺少有效 value", i+1)
			}
		}
		var e error
		root, e = patchAt(root, parts, p.Op, value)
		if e != nil {
			return nil, fmt.Errorf("第 %d 项 %s：%w", i+1, p.Path, e)
		}
	}
	result, e := json.Marshal(root)
	if e != nil {
		return nil, e
	}
	if _, _, _, e = Parse(result); e != nil {
		return nil, e
	}
	return result, nil
}
func patchAt(node any, path []string, op string, value any) (any, error) {
	key := path[0]
	last := len(path) == 1
	change := func(old any, exists bool) (any, bool, error) {
		if !last {
			if !exists {
				return nil, false, fmt.Errorf("父路径不存在")
			}
			next, e := patchAt(old, path[1:], op, value)
			return next, false, e
		}
		switch op {
		case "add":
			return value, false, nil
		case "replace":
			if exists {
				return value, false, nil
			}
		case "remove":
			if exists {
				return nil, true, nil
			}
		case "test":
			if exists && reflect.DeepEqual(old, value) {
				return old, false, nil
			}
			return nil, false, fmt.Errorf("test 不匹配")
		default:
			return nil, false, fmt.Errorf("仅支持 add/replace/remove/test")
		}
		return nil, false, fmt.Errorf("目标不存在")
	}
	switch n := node.(type) {
	case map[string]any:
		old, ok := n[key]
		v, remove, e := change(old, ok)
		if e != nil {
			return nil, e
		}
		if remove {
			delete(n, key)
		} else {
			n[key] = v
		}
		return n, nil
	case []any:
		idx, e := strconv.Atoi(key)
		if key == "-" && last && op == "add" {
			idx = len(n)
			e = nil
		}
		if e != nil || idx < 0 || idx > len(n) || key != "-" && strconv.Itoa(idx) != key {
			return nil, fmt.Errorf("数组下标无效")
		}
		exists := idx < len(n)
		var old any
		if exists {
			old = n[idx]
		}
		v, remove, e := change(old, exists)
		if e != nil {
			return nil, e
		}
		if last && op == "add" {
			n = append(n, nil)
			copy(n[idx+1:], n[idx:])
			n[idx] = v
		} else if remove {
			n = append(n[:idx], n[idx+1:]...)
		} else if exists {
			n[idx] = v
		} else {
			return nil, fmt.Errorf("目标不存在")
		}
		return n, nil
	default:
		return nil, fmt.Errorf("父路径不是对象或数组")
	}
}
