package deploy

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"logplatform/pkg/apidoc"
	"net/http"
	"sort"
)

type DocumentRequirement struct {
	ID         int64  `json:"id"`
	Source     string `json:"source,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Title      string `json:"title,omitempty"`
}
type DocumentChangeContext struct {
	WorkItem *DocumentRequirement           `json:"work_item,omitempty"`
	Fields   map[string]DocumentRequirement `json:"fields,omitempty"`
}
type DocumentFieldAddition struct {
	apidoc.DocumentField
	WorkItem *DocumentRequirement `json:"work_item,omitempty"`
}

// Existing clients continue sending the complete OpenAPI root. New clients may
// wrap that exact document with independent immutable requirement metadata.
func readDocumentSubmission(w http.ResponseWriter, r *http.Request, baseline ...[]byte) ([]byte, *DocumentChangeContext, error) {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, apidoc.MaxDocument+(256<<10)))
	if e != nil {
		return nil, nil, fmt.Errorf("文档超过大小限制或无法读取")
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return nil, nil, fmt.Errorf("文档 JSON 格式无效")
	}
	if root["openapi"] != nil || root["dubbo"] != nil {
		return raw, nil, nil
	}
	if root["patches"] != nil {
		if len(baseline) != 1 || root["document"] != nil {
			return nil, nil, fmt.Errorf("局部修改需要明确的原文档版本")
		}
		var patches []apidoc.Patch
		if json.Unmarshal(root["patches"], &patches) != nil {
			return nil, nil, fmt.Errorf("patches 格式无效")
		}
		candidate, e := apidoc.ApplyPatch(baseline[0], patches)
		if e != nil {
			return nil, nil, e
		}
		root["document"] = candidate
	}
	if len(root["document"]) == 0 {
		return nil, nil, fmt.Errorf("需要完整 OpenAPI 或 Dubbo 文档")
	}
	var context DocumentChangeContext
	if data := root["change_context"]; len(data) > 0 && string(data) != "null" {
		if len(data) > 128<<10 || json.Unmarshal(data, &context) != nil {
			return nil, nil, fmt.Errorf("需求关联格式无效或过大")
		}
		return root["document"], &context, nil
	}
	return root["document"], nil, nil
}
func (s *Store) resolveDocumentChanges(context *DocumentChangeContext, raw []byte) (*DocumentChangeContext, error) {
	if context == nil || (context.WorkItem == nil && len(context.Fields) == 0) {
		return nil, nil
	}
	if len(context.Fields) > 500 {
		return nil, fmt.Errorf("一次最多关联 500 个字段")
	}
	resolved := &DocumentChangeContext{Fields: map[string]DocumentRequirement{}}
	cache := map[int64]DocumentRequirement{}
	resolve := func(ref DocumentRequirement) (DocumentRequirement, error) {
		if ref.ID <= 0 {
			return ref, fmt.Errorf("请选择已登记的需求或 Bug 引用编号")
		}
		value, ok := cache[ref.ID]
		if !ok {
			w := s.GetWorkItem(ref.ID)
			if w == nil {
				return ref, fmt.Errorf("工作项 #%d 不存在，请先从实时查询中关联引用", ref.ID)
			}
			value = DocumentRequirement{ID: w.ID, Source: w.Source, ExternalID: w.ExternalID, Kind: w.Kind, Title: w.Title}
			cache[ref.ID] = value
		}
		if ref.Source != "" && (ref.Source != value.Source || ref.ExternalID != value.ExternalID || ref.Kind != value.Kind || ref.Title != value.Title) {
			return ref, fmt.Errorf("工作项 #%d 已变化，请重新预览关联", ref.ID)
		}
		return value, nil
	}
	if context.WorkItem != nil {
		v, e := resolve(*context.WorkItem)
		if e != nil {
			return nil, e
		}
		resolved.WorkItem = &v
	}
	fields, e := apidoc.DocumentFields(raw)
	if e != nil {
		return nil, e
	}
	pointers := map[string]bool{}
	for _, f := range fields {
		pointers[f.Pointer] = true
	}
	for pointer, ref := range context.Fields {
		if !pointers[pointer] {
			return nil, fmt.Errorf("关联路径不是当前文档字段：%s", pointer)
		}
		v, e := resolve(ref)
		if e != nil {
			return nil, e
		}
		resolved.Fields[pointer] = v
	}
	return resolved, nil
}
func (s *Store) documentChangeContext(id int64) (*DocumentChangeContext, error) {
	var raw string
	e := s.db.QueryRow(`SELECT document FROM api_document_change_contexts WHERE snapshot_id=?`, id).Scan(&raw)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var v DocumentChangeContext
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil, fmt.Errorf("文档需求关联记录无效")
	}
	return &v, nil
}
func documentFieldAdditions(before, after []byte, context *DocumentChangeContext) ([]DocumentFieldAddition, error) {
	fields, e := apidoc.AddedDocumentFields(before, after)
	if e != nil {
		return nil, e
	}
	pointers := map[string]bool{}
	out := []DocumentFieldAddition{}
	for _, f := range fields {
		v := DocumentFieldAddition{DocumentField: f}
		pointers[f.Pointer] = true
		if context != nil {
			v.WorkItem = context.WorkItem
			if ref, ok := context.Fields[f.Pointer]; ok {
				copy := ref
				v.WorkItem = &copy
			}
		}
		out = append(out, v)
	}
	if context != nil {
		for pointer := range context.Fields {
			if !pointers[pointer] {
				return nil, fmt.Errorf("字段 %s 不是本次新增字段，不能改写其需求来源", pointer)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}
func (s *Store) snapshotFieldAdditions(v APIDocumentSnapshot) ([]DocumentFieldAddition, *DocumentChangeContext, error) {
	var before []byte
	if v.BaselineID > 0 {
		base, e := s.apiDocumentSnapshot(v.ApplicationID, v.BaselineID)
		if e != nil {
			return nil, nil, e
		}
		if base.Environment != v.Environment {
			return nil, nil, fmt.Errorf("文档基线环境不匹配")
		}
		before = base.Document
	}
	context, e := s.documentChangeContext(v.ID)
	if e != nil {
		return nil, nil, e
	}
	added, e := documentFieldAdditions(before, v.Document, context)
	return added, context, e
}
