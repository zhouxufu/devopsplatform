package apidoc

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

type Entity struct {
	Key        string          `json:"key"`
	Kind       string          `json:"entity_kind"`
	Operation  string          `json:"operation"`
	Field      *DocumentField  `json:"field,omitempty"`
	Digest     string          `json:"digest"`
	Definition json.RawMessage `json:"definition,omitempty"`
}

// DocumentEntities separates each field's own definition from its children.
// Adding a nested property must not claim that every ancestor was modified.
func DocumentEntities(raw []byte) (map[string]Entity, error) {
	out := map[string]Entity{}
	if len(raw) == 0 {
		return out, nil
	}
	root, ops, e := decodeDocument(raw)
	if e != nil {
		return nil, e
	}
	add := func(v Entity, definition any) {
		b, _ := json.Marshal(definition)
		v.Digest = fmt.Sprintf("%x", sha256.Sum256(b))
		if len(b) <= 4096 {
			v.Definition = b
		}
		out[v.Key] = v
	}
	for _, op := range ops {
		var def any
		d := json.NewDecoder(strings.NewReader(string(op.Definition)))
		d.UseNumber()
		_ = d.Decode(&def)
		add(Entity{Key: "operation:" + op.ID, Kind: "operation", Operation: op.ID}, def)
	}
	fields, e := DocumentFields(raw)
	if e != nil {
		return nil, e
	}
	for _, f := range fields {
		value, _ := localPointer(root, f.Pointer)
		m, _ := value.(map[string]any)
		seen := map[string]bool{}
		for i := 0; i < 32; i++ {
			ref, _ := m["$ref"].(string)
			if !strings.HasPrefix(ref, "#/") || seen[ref] {
				break
			}
			seen[ref] = true
			v, ok := localPointer(root, ref)
			if !ok {
				break
			}
			target, _ := v.(map[string]any)
			copy := map[string]any{}
			for k, v := range target {
				copy[k] = v
			}
			for k, v := range m {
				if k != "$ref" {
					copy[k] = v
				}
			}
			m = copy
		}
		own := map[string]any{}
		for k, v := range m {
			if k != "properties" && k != "items" && k != "required" {
				own[k] = v
			}
		}
		if items, ok := m["items"].(map[string]any); ok {
			shallow := map[string]any{}
			for k, v := range items {
				if k != "properties" && k != "required" {
					shallow[k] = v
				}
			}
			own["items"] = shallow
		}
		parent := f.Pointer[:strings.LastIndex(f.Pointer, "/")]
		if strings.HasSuffix(parent, "/properties") {
			parent = strings.TrimSuffix(parent, "/properties")
			p, _ := localPointer(root, parent)
			pm, _ := p.(map[string]any)
			list, _ := pm["required"].([]any)
			name := strings.TrimPrefix(f.FieldPath, f.FieldPath[:strings.LastIndex(f.FieldPath, "/")+1])
			name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
			required := false
			for _, r := range list {
				if r == name {
					required = true
				}
			}
			own["required"] = required
		} else if v, ok := m["required"]; ok {
			own["required"] = v
		}
		copy := f
		add(Entity{Key: "field:" + f.Key(), Kind: "field", Operation: f.Operation, Field: &copy}, own)
	}
	// A referenced schema change is also a change to its consuming interface.
	for _, op := range ops {
		key := "operation:" + op.ID
		v := out[key]
		dependencies := map[string]string{}
		for k, f := range out {
			if f.Kind == "field" && f.Operation == op.ID {
				dependencies[k] = f.Digest
			}
		}
		b, _ := json.Marshal(struct {
			Definition json.RawMessage
			Fields     map[string]string
		}{op.Definition, dependencies})
		v.Digest = fmt.Sprintf("%x", sha256.Sum256(b))
		out[key] = v
	}
	return out, nil
}
