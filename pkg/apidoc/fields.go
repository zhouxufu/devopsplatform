package apidoc

import (
	"fmt"
	"sort"
	"strings"
)

// DocumentField identifies one parameter/schema field in an operation, with an
// actual JSON pointer for field-level requirement annotations. No examples or
// remote references are traversed. Shared schemas retain their source pointer.
type DocumentField struct {
	Operation string `json:"operation"`
	In        string `json:"in"`
	Status    string `json:"status,omitempty"`
	Media     string `json:"media,omitempty"`
	Name      string `json:"name"`
	FieldPath string `json:"field_path"`
	Pointer   string `json:"pointer"`
}

func (f DocumentField) Key() string {
	return f.Operation + "\x00" + f.In + "\x00" + f.Status + "\x00" + f.Media + "\x00" + f.FieldPath
}
func AddedDocumentFields(before, after []byte) ([]DocumentField, error) {
	previous, err := DocumentFields(before)
	if err != nil {
		return nil, err
	}
	current, err := DocumentFields(after)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, f := range previous {
		seen[f.Key()] = true
	}
	added := []DocumentField{}
	for _, f := range current {
		if !seen[f.Key()] {
			added = append(added, f)
		}
	}
	return added, nil
}
func DocumentFields(raw []byte) ([]DocumentField, error) {
	if len(raw) == 0 {
		return []DocumentField{}, nil
	}
	root, ops, err := decodeDocument(raw)
	if err != nil {
		return nil, err
	}
	out := []DocumentField{}
	seenFields := map[string]bool{}
	add := func(f DocumentField) error {
		if seenFields[f.Key()] {
			return nil
		}
		if len(out) >= 20000 {
			return fmt.Errorf("文档字段超过 20000 个，无法完成有界字段对比")
		}
		seenFields[f.Key()] = true
		out = append(out, f)
		return nil
	}
	object := func(v any) map[string]any { m, _ := v.(map[string]any); return m }
	resolve := func(v any, pointer string) (map[string]any, string) {
		m := object(v)
		seen := map[string]bool{}
		for i := 0; i < 32; i++ {
			ref, _ := m["$ref"].(string)
			if !strings.HasPrefix(ref, "#/") || seen[ref] {
				break
			}
			seen[ref] = true
			value, ok := localPointer(root, ref)
			if !ok {
				break
			}
			merged := map[string]any{}
			for k, v := range object(value) {
				merged[k] = v
			}
			for k, v := range m {
				if k != "$ref" {
					merged[k] = v
				}
			}
			m = merged
			pointer = ref
		}
		return m, pointer
	}
	var walk func(any, string, string, DocumentField, map[string]bool, int) error
	walk = func(schema any, pointer, prefix string, base DocumentField, refs map[string]bool, depth int) error {
		if depth > 32 {
			return nil
		}
		original := object(schema)
		ref, _ := original["$ref"].(string)
		if ref != "" && refs[ref] {
			return nil
		}
		next := map[string]bool{}
		for k, v := range refs {
			next[k] = v
		}
		if ref != "" {
			next[ref] = true
		}
		s, p := resolve(schema, pointer)
		if items, ok := s["items"]; ok {
			if err := walk(items, p+"/items", prefix, base, next, depth+1); err != nil {
				return err
			}
		}
		properties := object(s["properties"])
		for name, value := range properties {
			f := base
			f.Name = prefix + name
			f.FieldPath = base.FieldPath + "/" + pointerPart(name)
			f.Pointer = p + "/properties/" + pointerPart(name)
			if err := add(f); err != nil {
				return err
			}
			if err := walk(value, f.Pointer, f.Name+".", f, next, depth+1); err != nil {
				return err
			}
		}
		for _, key := range []string{"allOf", "oneOf", "anyOf"} {
			if variants, ok := s[key].([]any); ok {
				for i, v := range variants {
					if err := walk(v, fmt.Sprintf("%s/%s/%d", p, key, i), prefix, base, next, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	paths := object(root["paths"])
	for _, op := range ops {
		item := object(paths[op.Path])
		def := object(item[strings.ToLower(op.Method)])
		base := DocumentField{Operation: op.ID}
		opPointer := "#/paths/" + pointerPart(op.Path) + "/" + strings.ToLower(op.Method)
		// Operation parameters override path parameters by name + location.
		params := map[string]DocumentField{}
		schemas := map[string]any{}
		for _, scope := range []struct {
			value   map[string]any
			pointer string
		}{{item, "#/paths/" + pointerPart(op.Path)}, {def, opPointer}} {
			values, _ := scope.value["parameters"].([]any)
			for i, value := range values {
				p, pointer := resolve(value, fmt.Sprintf("%s/parameters/%d", scope.pointer, i))
				name, _ := p["name"].(string)
				in, _ := p["in"].(string)
				if name == "" || in == "" {
					continue
				}
				f := base
				f.Name = name
				f.FieldPath = "/" + pointerPart(name)
				f.In = in
				f.Pointer = pointer
				key := in + "\x00" + name
				params[key] = f
				schemas[key] = p["schema"]
			}
		}
		for key, f := range params {
			if err := add(f); err != nil {
				return nil, err
			}
			if err := walk(schemas[key], f.Pointer+"/schema", f.Name+".", f, map[string]bool{}, 0); err != nil {
				return nil, err
			}
		}
		body, bp := resolve(def["requestBody"], opPointer+"/requestBody")
		for media, value := range object(body["content"]) {
			f := base
			f.In = "body"
			f.Media = media
			if err := walk(object(value)["schema"], bp+"/content/"+pointerPart(media)+"/schema", "", f, map[string]bool{}, 0); err != nil {
				return nil, err
			}
		}
		for status, value := range object(def["responses"]) {
			response, rp := resolve(value, opPointer+"/responses/"+pointerPart(status))
			for media, content := range object(response["content"]) {
				f := base
				f.In = "response"
				f.Status = status
				f.Media = media
				if err := walk(object(content)["schema"], rp+"/content/"+pointerPart(media)+"/schema", "", f, map[string]bool{}, 0); err != nil {
					return nil, err
				}
			}
		}
	}
	// Stabilize metadata and previews independently of Go map traversal order.
	sortDocumentFields(out)
	return out, nil
}
func sortDocumentFields(fields []DocumentField) {
	// Keep the JSON serializer stable without coupling identity to array position.
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key() < fields[j].Key() })
}
