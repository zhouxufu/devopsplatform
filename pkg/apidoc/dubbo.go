package apidoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// RPCMethod is a native Dubbo contract, not a fabricated HTTP operation.
// Parameter order and erased Java types form the overload identity.
type RPCParameter struct {
	Name        string          `json:"name"`
	JavaType    string          `json:"java_type"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Example     json.RawMessage `json:"example,omitempty"`
}
type RPCMethod struct {
	Interface   string         `json:"interface"`
	Method      string         `json:"method"`
	Group       string         `json:"group,omitempty"`
	Version     string         `json:"version,omitempty"`
	Summary     string         `json:"summary"`
	Description string         `json:"description,omitempty"`
	Maintainer  string         `json:"x-maintainer,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Parameters  []RPCParameter `json:"parameters"`
	Returns     RPCParameter   `json:"returns"`
	Exceptions  []string       `json:"exceptions,omitempty"`
}

var JavaName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$`)

func ValidJavaType(s string) bool {
	if len(s) > 512 {
		return false
	}
	dimensions := 0
	for strings.HasSuffix(s, "[]") {
		s = strings.TrimSuffix(s, "[]")
		dimensions++
	}
	return dimensions <= 8 && !(s == "void" && dimensions > 0) && JavaName.MatchString(s)
}

func (m RPCMethod) ParameterTypes() []string {
	out := []string{}
	for _, p := range m.Parameters {
		out = append(out, p.JavaType)
	}
	return out
}
func (m RPCMethod) Signature() string {
	return m.Interface + "#" + m.Method + "(" + strings.Join(m.ParameterTypes(), ",") + ")"
}
func (m RPCMethod) ID() string {
	// JSON tuple avoids ambiguous delimiter/group/version identities.
	b, _ := json.Marshal([]any{m.Interface, m.Group, m.Version, m.Method, m.ParameterTypes()})
	h := sha256.Sum256(b)
	return "dubbo:" + hex.EncodeToString(h[:])
}
func (d Document) Protocol() string {
	if d.Dubbo != "" {
		return "dubbo"
	}
	return "http"
}
func parseDubbo(raw []byte, d *Document) (*Document, []Operation, string, error) {
	if d.Dubbo != "1.0" || d.OpenAPI != "" || len(d.Paths) > 0 || d.Info.Title == "" || d.Info.Version == "" || len(d.Methods) == 0 || len(d.Methods) > 5000 {
		return nil, nil, "", fmt.Errorf("Dubbo 文档需包含 dubbo:1.0、info 和 1–5000 个 methods")
	}
	seen := map[string]bool{}
	ops := []Operation{}
	for _, m := range d.Methods {
		if len(m.Interface) > 512 || !JavaName.MatchString(m.Interface) || !strings.Contains(m.Interface, ".") || !JavaName.MatchString(m.Method) || strings.Contains(m.Method, ".") || len(m.Method) > 128 || len(m.Group) > 191 || len(m.Version) > 191 || strings.ContainsAny(m.Group+m.Version, "\x00\r\n*/?&") || len(m.Parameters) > 32 || !ValidJavaType(m.Returns.JavaType) {
			return nil, nil, "", fmt.Errorf("Dubbo 方法签名、分组、版本或返回类型无效")
		}
		names := map[string]bool{}
		for _, p := range m.Parameters {
			if p.Name == "" || len(p.Name) > 128 || names[p.Name] || !ValidJavaType(p.JavaType) || p.JavaType == "void" {
				return nil, nil, "", fmt.Errorf("Dubbo %s 的参数名称或 Java 类型无效", m.Method)
			}
			names[p.Name] = true
		}
		id := m.ID()
		if seen[id] {
			return nil, nil, "", fmt.Errorf("Dubbo 方法签名重复：%s", m.Signature())
		}
		seen[id] = true
		def, _ := json.Marshal(m)
		copy := m
		ops = append(ops, Operation{ID: id, Protocol: "dubbo", RPC: &copy, Method: "DUBBO", Path: m.Signature(), Summary: m.Summary, Description: m.Description, Maintainer: m.Maintainer, Tags: m.Tags, Definition: def})
	}
	var canonical any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	_ = dec.Decode(&canonical)
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(b)
	return d, ops, "sha256:" + hex.EncodeToString(sum[:]), nil
}
