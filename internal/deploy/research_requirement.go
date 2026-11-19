package deploy

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Native requirements have a development stage, not the Bug resolution state.
// Only an actual story edit form exposing developed authorizes this adapter.
func (z *zentaoLegacy) researchReadItem(ctx context.Context, w *WorkItem, method string) (*ResearchRemote, error) {
	if w.Kind != "requirement" {
		if method == "comment" {
			method = "edit"
		}
		return z.researchRead(ctx, w.ExternalID, method)
	}
	if method == "develop" || method == "comment" {
		method = "edit"
	}
	if method != "view" && method != "edit" {
		return nil, fmt.Errorf("外部需求仅支持读取、开发完成及备注；验收／关闭由来源平台处理")
	}
	z.queryMu.Lock()
	defer z.queryMu.Unlock()
	z.resetLiveSearch()
	read := func(method string) (*ResearchRemote, error) {
		v := &ResearchRemote{}
		if e := z.call(ctx, "story", method, url.Values{"storyID": {w.ExternalID}}, nil, v); e != nil {
			return nil, e
		}
		if v.Story == nil || researchValue(v.Story, "id") != w.ExternalID {
			return nil, fmt.Errorf("来源未返回可访问的需求")
		}
		v.Bug = v.Story // Shared read-only presentation/receipt envelope.
		return v, nil
	}
	v, e := read(method)
	if e != nil {
		return nil, e
	}
	canonical := v
	if method != "view" {
		canonical, e = read("view")
		if e != nil {
			return nil, e
		}
	}
	v.Digest = digestJSON(canonical.Story)
	return v, nil
}
func requirementDevelopedLabel(v *ResearchRemote) string {
	if label := v.StageList["developed"]; label != "" {
		return label
	}
	if field, ok := v.Fields["stage"].(map[string]any); ok {
		if options, ok := field["options"].(map[string]any); ok {
			if label, ok := options["developed"].(string); ok {
				return label
			}
		}
	}
	return ""
}
func requirementEditForm(before *ResearchRemote, action string) (url.Values, error) {
	if before.Story == nil {
		return nil, fmt.Errorf("缺少原需求快照")
	}
	if action != "develop" && action != "comment" {
		return nil, fmt.Errorf("不支持此需求回写操作")
	}
	form := url.Values{}
	// Preserve native edit values. Title/spec/verify are not rewritten as a Bug.
	for _, key := range []string{"product", "branch", "module", "plan", "parent", "source", "sourceNote", "fromBug", "title", "spec", "verify", "type", "pri", "estimate", "status", "stage", "category", "assignedTo", "mailto", "keywords", "version", "reviewedBy", "reviewedDate", "closedBy", "closedDate", "closedReason", "duplicateStory", "linkStories", "linkRequirements", "childStories", "notifyEmail", "feedbackBy", "grade", "lastEditedDate"} {
		if value, ok := before.Story[key]; ok && value != nil {
			if key == "linkStories" || key == "linkRequirements" || key == "plan" {
				if text, ok := value.(string); ok {
					for _, id := range strings.Split(text, ",") {
						if id != "" {
							form.Add(key+"[]", id)
						}
					}
					continue
				}
			}
			switch v := value.(type) {
			case []any:
				for _, x := range v {
					form.Add(key+"[]", fmt.Sprint(x))
				}
			case map[string]any:
				return nil, fmt.Errorf("需求编辑字段 %s 为结构化值，需要匹配来源适配器", key)
			default:
				form.Set(key, fmt.Sprint(v))
			}
		}
	}
	if action == "develop" {
		form.Set("stage", "developed")
	}
	return form, nil
}
