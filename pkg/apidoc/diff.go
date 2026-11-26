package apidoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type ChangeSummary struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Modified  int `json:"modified"`
	Unchanged int `json:"unchanged"`
}
type FieldChange struct {
	Path          string `json:"path"`
	Before        string `json:"before"`
	After         string `json:"after"`
	BeforePresent bool   `json:"before_present"`
	AfterPresent  bool   `json:"after_present"`
}
type OperationChange struct {
	ID        string        `json:"id"`
	Method    string        `json:"method"`
	Path      string        `json:"path"`
	Summary   string        `json:"summary"`
	Kind      string        `json:"kind"`
	Fields    []FieldChange `json:"fields"`
	Truncated bool          `json:"truncated"`
}
type Comparison struct {
	Summary   ChangeSummary     `json:"summary"`
	Changes   []OperationChange `json:"changes"`
	Truncated bool              `json:"truncated"`
}

func decodeDocument(raw []byte) (map[string]any, []Operation, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil, nil
	}
	_, ops, _, e := Parse(raw)
	if e != nil {
		return nil, nil, e
	}
	var root map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	e = d.Decode(&root)
	return root, ops, e
}
func fieldText(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 2048 {
		return string(b[:2048]) + "…（已截断）"
	}
	return string(b)
}
func pointerPart(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}
func collectFields(path string, before any, bp bool, after any, ap bool, c *OperationChange) {
	if bp == ap && reflect.DeepEqual(before, after) {
		return
	}
	if len(c.Fields) >= 100 {
		c.Truncated = true
		return
	}
	b, bok := before.(map[string]any)
	a, aok := after.(map[string]any)
	bounded := len(path) < 2048 && strings.Count(path, "/") < 64
	if (!bp || bok) && (!ap || aok) && (bok || aok) && (len(b) > 0 || len(a) > 0) && bounded {
		keys := map[string]bool{}
		for k := range b {
			keys[k] = true
		}
		for k := range a {
			keys[k] = true
		}
		sorted := []string{}
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			bv, bp := b[k]
			av, ap := a[k]
			collectFields(path+"/"+pointerPart(k), bv, bp, av, ap, c)
		}
		return
	}
	ba, bArray := before.([]any)
	aa, aArray := after.([]any)
	if (!bp || bArray) && (!ap || aArray) && (bArray || aArray) && (len(ba) > 0 || len(aa) > 0) && bounded {
		count := max(len(ba), len(aa))
		for i := 0; i < count; i++ {
			var bv, av any
			bp, ap := i < len(ba), i < len(aa)
			if bp {
				bv = ba[i]
			}
			if ap {
				av = aa[i]
			}
			collectFields(path+"/"+strconv.Itoa(i), bv, bp, av, ap, c)
			if len(c.Fields) >= 100 && i+1 < count {
				c.Truncated = true
				break
			}
		}
		return
	}
	if len(path) > 2048 {
		path = path[:2048] + "…"
		c.Truncated = true
	}
	c.Fields = append(c.Fields, FieldChange{Path: path, Before: fieldText(before), After: fieldText(after), BeforePresent: bp, AfterPresent: ap})
}
func localPointer(root any, ref string) (any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	v := root
	for _, part := range strings.Split(ref[2:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			v, ok = x[key]
			if !ok {
				return nil, false
			}
		case []any:
			i, e := strconv.Atoi(key)
			if e != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}
func refsIn(v any, refs map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "$ref" {
				if s, ok := v.(string); ok && strings.HasPrefix(s, "#/") {
					refs[s] = true
				}
			} else {
				refsIn(v, refs)
			}
		}
	case []any:
		for _, v := range x {
			refsIn(v, refs)
		}
	}
}

type contractIndex struct {
	root   map[string]any
	values map[string]any
	refs   map[string][]string
	hashes map[string][32]byte
}

func newContractIndex(root map[string]any) *contractIndex {
	return &contractIndex{root: root, values: map[string]any{}, refs: map[string][]string{}, hashes: map[string][32]byte{}}
}
func (x *contractIndex) dependencies(v any) (map[string]any, error) {
	pending := map[string]bool{}
	refsIn(v, pending)
	seen := map[string]any{}
	for len(pending) > 0 {
		var ref string
		for key := range pending {
			ref = key
			break
		}
		delete(pending, ref)
		if _, ok := seen[ref]; ok {
			continue
		}
		if len(seen) >= 2000 {
			return nil, fmt.Errorf("接口关联定义超过 2000 个，无法完成有界对比")
		}
		value, cached := x.values[ref]
		if !cached {
			value, _ = localPointer(x.root, ref)
			x.values[ref] = value
			children := map[string]bool{}
			refsIn(value, children)
			for child := range children {
				x.refs[ref] = append(x.refs[ref], child)
			}
			raw, _ := json.Marshal(value)
			x.hashes[ref] = sha256.Sum256(raw)
		}
		seen[ref] = value
		for _, child := range x.refs[ref] {
			pending[child] = true
		}
	}
	return seen, nil
}
func operationContract(root map[string]any, op Operation) any {
	if op.Protocol == "dubbo" {
		var v any
		d := json.NewDecoder(bytes.NewReader(op.Definition))
		d.UseNumber()
		_ = d.Decode(&v)
		return v
	}
	paths, _ := root["paths"].(map[string]any)
	item, _ := paths[op.Path].(map[string]any)
	definition, _ := item[strings.ToLower(op.Method)].(map[string]any)
	v := map[string]any{"operation": definition}
	if p, ok := item["parameters"]; ok {
		v["pathParameters"] = p
	}
	if _, ok := definition["security"]; !ok {
		if security, ok := root["security"]; ok {
			v["security"] = security
		}
	}
	security, exists := definition["security"]
	if !exists {
		security = root["security"]
	}
	schemes := map[string]any{}
	if rules, ok := security.([]any); ok {
		for _, rule := range rules {
			if names, ok := rule.(map[string]any); ok {
				for name := range names {
					if value, ok := localPointer(root, "#/components/securitySchemes/"+pointerPart(name)); ok {
						schemes[name] = value
					}
				}
			}
		}
	}
	if len(schemes) > 0 {
		v["securitySchemes"] = schemes
	}
	return v
}

// Compare reports structural API changes; it does not classify backwards compatibility.
// Shared local references are tracked with cycle detection, without remote resolution.
func Compare(before, after []byte) (Comparison, error) {
	out := Comparison{Changes: []OperationChange{}}
	b, bo, e := decodeDocument(before)
	if e != nil {
		return out, e
	}
	a, ao, e := decodeDocument(after)
	if e != nil {
		return out, e
	}
	old, new := map[string]Operation{}, map[string]Operation{}
	ids := map[string]bool{}
	for _, op := range bo {
		old[op.ID] = op
		ids[op.ID] = true
	}
	for _, op := range ao {
		new[op.ID] = op
		ids[op.ID] = true
	}
	sorted := []string{}
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	bi, ai := newContractIndex(b), newContractIndex(a)
	remainingBytes := 1 << 20
	for _, id := range sorted {
		prev, bp := old[id]
		next, ap := new[id]
		op := next
		if !ap {
			op = prev
		}
		summary := op.Summary
		if len(summary) > 512 {
			summary = summary[:512] + "…"
		}
		c := OperationChange{ID: id, Method: op.Method, Path: op.Path, Summary: summary, Fields: []FieldChange{}}
		switch {
		case !bp:
			c.Kind = "added"
			out.Summary.Added++
		case !ap:
			c.Kind = "removed"
			out.Summary.Removed++
		default:
			bc, ac := operationContract(b, prev), operationContract(a, next)
			collectFields("", bc, true, ac, true, &c)
			bd, e := bi.dependencies(bc)
			if e != nil {
				return out, e
			}
			ad, e := ai.dependencies(ac)
			if e != nil {
				return out, e
			}
			refs := map[string]bool{}
			for ref := range bd {
				refs[ref] = true
			}
			for ref := range ad {
				refs[ref] = true
			}
			ordered := []string{}
			for ref := range refs {
				ordered = append(ordered, ref)
			}
			sort.Strings(ordered)
			for _, ref := range ordered {
				bv, bp := bd[ref]
				av, ap := ad[ref]
				if !bp || !ap || bi.hashes[ref] != ai.hashes[ref] {
					collectFields(ref, bv, bp, av, ap, &c)
				}
			}
			if len(c.Fields) == 0 && !c.Truncated {
				out.Summary.Unchanged++
				continue
			}
			c.Kind = "modified"
			out.Summary.Modified++
		}
		raw, _ := json.Marshal(c)
		for len(raw) > remainingBytes && len(c.Fields) > 0 {
			c.Fields = c.Fields[:len(c.Fields)-1]
			c.Truncated = true
			out.Truncated = true
			raw, _ = json.Marshal(c)
		}
		if len(raw) > remainingBytes {
			out.Truncated = true
			continue
		}
		remainingBytes -= len(raw)

		if len(out.Changes) < 500 {
			out.Changes = append(out.Changes, c)
		} else {
			out.Truncated = true
		}
	}
	return out, nil
}
