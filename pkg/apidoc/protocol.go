// Package apidoc defines vendor-independent document generation plans and artifacts.
// Providers prepare data, never shell scripts; the build Runner owns execution.
package apidoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const Capability = "api.document.generate.v1"
const MaxDocument = 8 << 20

type Source struct {
	OnBuild  bool            `json:"on_build"`
	Provider string          `json:"provider"`
	Required bool            `json:"required"`
	Settings json.RawMessage `json:"settings"`
	Revision int64           `json:"revision"`
}
type PlanInput struct {
	Tool     string          `json:"tool"`
	Settings json.RawMessage `json:"settings"`
}
type Plan struct {
	ProviderDigest  string            `json:"provider_digest,omitempty"`
	Version         int               `json:"version"`
	Provider        string            `json:"provider"`
	ProviderVersion string            `json:"provider_version"`
	Tool            string            `json:"tool"`
	Tasks           []string          `json:"tasks"`
	Properties      map[string]string `json:"properties"`
	InputFiles      []string          `json:"input_files"`
	OutputFile      string            `json:"output_file"`
	Format          string            `json:"format"`
}
type Frozen struct {
	Environment string `json:"environment,omitempty"`
	Source      Source `json:"source"`
	Plan        *Plan  `json:"plan,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

var taskRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]{0,250}$`)
var propertyRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,100}$`)

func RelativeFile(v string) bool {
	return len(v) > 0 && len(v) < 512 && v != "." && path.Clean(v) == v && !strings.HasPrefix(v, "/") && v != ".." && !strings.HasPrefix(v, "../") && !strings.ContainsAny(v, "\\:\x00\r\n")
}
func ValidatePlan(p Plan) error {
	if p.Version != 1 || p.Provider == "" || p.ProviderVersion == "" || p.Tool != "maven" && p.Tool != "gradle" || p.Format != "openapi-json" || !RelativeFile(p.OutputFile) {
		return fmt.Errorf("文档插件返回了不兼容的生成计划")
	}
	// Outputs belong to generated build directories; never delete a tracked source document.
	if !strings.HasPrefix(p.OutputFile, "target/") && !strings.HasPrefix(p.OutputFile, "build/") {
		return fmt.Errorf("文档输出必须位于 target/ 或 build/ 生成目录")
	}
	if len(p.Tasks) == 0 || len(p.Tasks) > 12 || len(p.Properties) > 20 || len(p.InputFiles) > 8 {
		return fmt.Errorf("文档生成计划过大")
	}
	for _, s := range p.Tasks {
		if !taskRE.MatchString(s) {
			return fmt.Errorf("文档生成任务无效")
		}
	}
	for k, v := range p.Properties {
		if !propertyRE.MatchString(k) || len(v) > 1024 || strings.ContainsAny(v, "\x00\r\n") {
			return fmt.Errorf("文档生成属性无效")
		}
	}
	for _, s := range p.InputFiles {
		if !RelativeFile(s) || s == p.OutputFile {
			return fmt.Errorf("文档输入路径无效")
		}
	}
	return nil
}

type Operation struct {
	ID             string          `json:"id"`
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Summary        string          `json:"summary"`
	Description    string          `json:"description,omitempty"`
	Maintainer     string          `json:"maintainer,omitempty"`
	MaintainerName string          `json:"maintainer_name,omitempty"`
	Tags           []string        `json:"tags"`
	SSE            bool            `json:"sse"`
	Definition     json.RawMessage `json:"definition"`
}
type Document struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string          `json:"title"`
		Version string          `json:"version"`
		Contact json.RawMessage `json:"contact,omitempty"`
	} `json:"info"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components json.RawMessage                       `json:"components,omitempty"`
}

func Parse(raw []byte) (*Document, []Operation, string, error) {
	if len(raw) == 0 || len(raw) > MaxDocument {
		return nil, nil, "", fmt.Errorf("文档为空或超过 8 MiB")
	}
	var d Document
	if json.Unmarshal(raw, &d) != nil || !(strings.HasPrefix(d.OpenAPI, "3.0.") || strings.HasPrefix(d.OpenAPI, "3.1.")) || d.Info.Title == "" || d.Info.Version == "" || d.Paths == nil || len(d.Paths) > 2000 {
		return nil, nil, "", fmt.Errorf("请选择有效的 OpenAPI 3.0/3.1 JSON 文档（需包含标题、版本与 paths）")
	}
	ops := []Operation{}
	for p, methods := range d.Paths {
		if !strings.HasPrefix(p, "/") || len(p) > 8192 {
			return nil, nil, "", fmt.Errorf("接口路径必须以 / 开头")
		}
		for method, def := range methods {
			if !map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}[method] {
				continue
			}
			var v struct {
				OperationID string          `json:"operationId"`
				Summary     string          `json:"summary"`
				Description string          `json:"description"`
				Maintainer  json.RawMessage `json:"x-maintainer"`
				Tags        []string        `json:"tags"`
				Responses   map[string]struct {
					Content map[string]json.RawMessage `json:"content"`
				} `json:"responses"`
			}
			if json.Unmarshal(def, &v) != nil || len(v.Responses) == 0 {
				return nil, nil, "", fmt.Errorf("接口 %s %s 缺少有效 responses", method, p)
			}
			stream := false
			for _, r := range v.Responses {
				if _, ok := r.Content["text/event-stream"]; ok {
					stream = true
				}
			}
			maintainer := maintainerName(v.Maintainer)
			if maintainer == "" {
				var contact struct {
					Name json.RawMessage `json:"name"`
				}
				if json.Unmarshal(d.Info.Contact, &contact) == nil {
					maintainer = maintainerName(contact.Name)
				}
			}
			ops = append(ops, Operation{ID: method + " " + p, Method: strings.ToUpper(method), Path: p, Summary: v.Summary, Description: v.Description, Maintainer: maintainer, Tags: v.Tags, SSE: stream, Definition: def})
		}
	}
	if len(ops) > 5000 {
		return nil, nil, "", fmt.Errorf("接口数量超过 5000")
	}
	// Canonical JSON digest ignores insignificant whitespace, but preserves all source fields.
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&canonical)
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(b)
	return &d, ops, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Optional human metadata never changes protocol identity or invents ownership
// from the importer, generator process or an unrelated contact email.
func maintainerName(raw json.RawMessage) string {
	var name string
	if json.Unmarshal(raw, &name) != nil {
		return ""
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 200 || strings.ContainsAny(name, "\x00\r\n") {
		return ""
	}
	return name
}

// ExplicitMaintainer excludes document-wide defaults, so reporting an unchanged
// operation cannot silently transfer it when another user uploads the full file.
func ExplicitMaintainer(op Operation) string {
	var v struct {
		Maintainer json.RawMessage `json:"x-maintainer"`
	}
	if json.Unmarshal(op.Definition, &v) != nil {
		return ""
	}
	return maintainerName(v.Maintainer)
}
