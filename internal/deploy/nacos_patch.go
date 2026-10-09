package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type NacosDiff struct {
	Key    string  `json:"key"`
	Before *string `json:"before"`
	After  *string `json:"after"`
}

var nacosKeyRE = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)
var nacosPropertyRE = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*[=:]\s*(.*)$`)

func validateNacosTree(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
		return fmt.Errorf("配置含 YAML 锚点、别名或合并键，需人工处理，不自动改写")
	}
	if n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
		return fmt.Errorf("配置含自定义 YAML 类型，需人工处理，不自动改写")
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
				return fmt.Errorf("配置含重复或非字符串键，不自动改写")
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := validateNacosTree(c); err != nil {
			return err
		}
	}
	return nil
}

// Dotted keys address object/mapping paths, never arrays. Values are JSON
// scalars (true, 123, null, quoted strings) or unquoted plain strings.
func nacosValue(value string) (*yaml.Node, error) {
	var v any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if json.Valid([]byte(value)) {
		if err := decoder.Decode(&v); err != nil {
			return nil, fmt.Errorf("配置值无效")
		}
	} else {
		v = value
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str"}
	switch x := v.(type) {
	case nil:
		n.Tag, n.Value = "!!null", "null"
	case bool:
		n.Tag, n.Value = "!!bool", fmt.Sprint(x)
	case json.Number:
		n.Value = x.String()
		n.Tag = "!!int"
		if strings.ContainsAny(n.Value, ".eE") {
			n.Tag = "!!float"
		}
	case string:
		n.Value = x
	default:
		return nil, fmt.Errorf("配置值只支持字符串、数字、布尔值或 null；不整体替换对象和数组")
	}
	return n, nil
}
func nacosNodeValue(n *yaml.Node) *string {
	if n == nil {
		return nil
	}
	v := n.Value
	if n.Tag == "!!str" {
		b, _ := json.Marshal(v)
		v = string(b)
	}
	return &v
}
func patchNacosContent(content string, change *NacosChange) (string, []NacosDiff, error) {
	if len(content) > nacosMaxContent || len(change.Entries) > 200 {
		return "", nil, fmt.Errorf("Nacos 配置过大")
	}
	for _, e := range change.Entries {
		if !nacosKeyRE.MatchString(e.Key) {
			return "", nil, fmt.Errorf("配置项使用字母、数字、短横线、下划线，点表示层级")
		}
	}
	if change.Format == "properties" {
		return patchNacosProperties(content, change.Entries)
	}
	if change.Format == "json" && !json.Valid([]byte(content)) {
		return "", nil, fmt.Errorf("目标配置不是有效 JSON")
	}
	var doc yaml.Node
	d := yaml.NewDecoder(strings.NewReader(content))
	if err := d.Decode(&doc); err != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", nil, fmt.Errorf("目标配置必须是单个 YAML/JSON 对象")
	}
	var second yaml.Node
	if d.Decode(&second) != io.EOF {
		return "", nil, fmt.Errorf("不支持多文档或尾部无效 YAML")
	}
	if err := validateNacosTree(&doc); err != nil {
		return "", nil, err
	}
	diffs := []NacosDiff{}
	for _, e := range change.Entries {
		node := doc.Content[0]
		parts := strings.Split(e.Key, ".")
		for pathIndex, part := range parts[:len(parts)-1] {
			for j := 0; j < len(node.Content); j += 2 {
				if node.Content[j].Value == strings.Join(parts[pathIndex:], ".") {
					return "", nil, fmt.Errorf("配置项存在字面点键与层级路径歧义，需人工处理")
				}
			}
			var child *yaml.Node
			for i := 0; i < len(node.Content); i += 2 {
				if node.Content[i].Value == part {
					child = node.Content[i+1]
					break
				}
			}
			if child == nil && e.Action == "set" {
				child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part}, child)
			}
			if child == nil {
				node = nil
				break
			}
			if child.Kind != yaml.MappingNode {
				return "", nil, fmt.Errorf("配置路径 %s 穿过标量或数组，不自动覆盖", e.Key)
			}
			node = child
		}
		if node == nil {
			continue
		}
		key := parts[len(parts)-1]
		index := -1
		var old *yaml.Node
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				index = i
				old = node.Content[i+1]
				break
			}
		}
		if old != nil && old.Kind != yaml.ScalarNode {
			return "", nil, fmt.Errorf("配置项 %s 是对象或数组，不整体替换或删除", e.Key)
		}
		var next *yaml.Node
		if e.Action == "set" {
			var err error
			next, err = nacosValue(e.Value)
			if err != nil {
				return "", nil, err
			}
		}
		before, after := nacosNodeValue(old), nacosNodeValue(next)
		if (before == nil && after == nil) || (old != nil && next != nil && old.Tag == next.Tag && old.Value == next.Value) {
			continue
		}
		diffs = append(diffs, NacosDiff{e.Key, before, after})
		if next == nil {
			node.Content = append(node.Content[:index], node.Content[index+2:]...)
		} else if index >= 0 {
			next.HeadComment, next.LineComment, next.FootComment = old.HeadComment, old.LineComment, old.FootComment
			node.Content[index+1] = next
		} else {
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, next)
		}
	}
	if len(diffs) == 0 {
		return content, diffs, nil
	}
	var out []byte
	var err error
	if change.Format == "json" {
		var v any
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		_ = decoder.Decode(&v)
		root := v.(map[string]any)
		for _, e := range change.Entries {
			node := root
			parts := strings.Split(e.Key, ".")
			missing := false
			for _, part := range parts[:len(parts)-1] {
				child, ok := node[part].(map[string]any)
				if !ok {
					if e.Action == "delete" {
						missing = true
						break
					}
					child = map[string]any{}
					node[part] = child
				}
				node = child
			}
			if missing {
				continue
			}
			key := parts[len(parts)-1]
			if e.Action == "delete" {
				delete(node, key)
			} else {
				var value any
				decoder := json.NewDecoder(strings.NewReader(e.Value))
				decoder.UseNumber()
				if !json.Valid([]byte(e.Value)) {
					value = e.Value
				} else {
					_ = decoder.Decode(&value)
				}
				node[key] = value
			}
		}
		out, err = json.MarshalIndent(root, "", "  ")
	} else {
		var b bytes.Buffer
		encoder := yaml.NewEncoder(&b)
		encoder.SetIndent(2)
		err = encoder.Encode(&doc)
		_ = encoder.Close()
		out = b.Bytes()
	}
	if err != nil || len(out) > nacosMaxContent {
		return "", nil, fmt.Errorf("配置序列化失败或超过大小限制")
	}
	return string(out), diffs, nil
}

func patchNacosProperties(content string, entries []NacosEntry) (string, []NacosDiff, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	indexes := map[string]int{}
	values := map[string]string{}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") {
			continue
		}
		m := nacosPropertyRE.FindStringSubmatch(line)
		if m == nil || strings.ContainsAny(line, "\\\r") {
			return "", nil, fmt.Errorf("properties 仅支持无转义、无续行的 key=value/key:value；复杂格式需人工处理")
		}
		if _, ok := indexes[m[1]]; ok {
			return "", nil, fmt.Errorf("properties 含重复键，不自动改写")
		}
		indexes[m[1]] = i
		values[m[1]] = m[2]
	}
	diffs := []NacosDiff{}
	for _, e := range entries {
		if strings.ContainsAny(e.Value, "\r\n\\") {
			return "", nil, fmt.Errorf("properties 值暂不支持转义、换行或续行")
		}
		old, exists := values[e.Key]
		var before, after *string
		if exists {
			v := old
			before = &v
		}
		if e.Action == "set" {
			v := e.Value
			after = &v
		}
		if (!exists && after == nil) || (exists && after != nil && old == *after) {
			continue
		}
		diffs = append(diffs, NacosDiff{e.Key, before, after})
		if e.Action == "delete" {
			lines[indexes[e.Key]] = ""
		} else if exists {
			lines[indexes[e.Key]] = e.Key + "=" + e.Value
		} else {
			lines = append(lines, e.Key+"="+e.Value)
		}
	}
	if len(diffs) == 0 {
		return content, diffs, nil
	}
	out := strings.Join(lines, "\n")
	if len(out) > nacosMaxContent {
		return "", nil, fmt.Errorf("配置超过大小限制")
	}
	return out, diffs, nil
}
