package platform

import (
	"bytes"
	"encoding/json"
	"io"
	p "logplatform/pkg/extensionprotocol"
	rules "logplatform/pkg/releasepolicy"
	"net/http"
	"strings"
)

// Compile at configuration boundaries, never while rendering a row or executing
// a release. Only the bounded declarative result crosses into delivery storage.
func (g *Gateway) compileReleasePolicy(w http.ResponseWriter, r *http.Request, user, role string) bool {
	if !(r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/release-rules") || r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/release-rules/preview")) {
		return true
	}
	if role != RoleAdmin {
		writeJSONStatus(w, 403, map[string]string{"error": "仅管理员可维护上线识别规则"})
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		writeJSONStatus(w, 400, map[string]string{"error": "规则配置过大或无法读取"})
		return false
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil {
		writeJSONStatus(w, 400, map[string]string{"error": "规则格式无效"})
		return false
	}
	var provider string
	json.Unmarshal(document["provider"], &provider)
	if provider != "" && provider != "builtin" {
		if g.extensions == nil {
			writeJSONStatus(w, 503, map[string]string{"error": "规则插件不可用，未保存配置"})
			return false
		}
		input, _ := json.Marshal(map[string]json.RawMessage{"policy": document["policy"]})
		result, err := g.extensions.Invoke(r.Context(), provider, p.Request{Capability: rules.Capability, Operation: "compile", Actor: p.Actor{User: user, Role: role}, Input: input}, false)
		if err != nil || result.Error != nil {
			writeJSONStatus(w, 409, map[string]string{"error": "规则插件不可用或拒绝此配置，请检查插件状态和规则；未保存"})
			return false
		}
		var compiled struct {
			Policy *rules.Policy `json:"policy"`
		}
		if json.Unmarshal(result.Data, &compiled) != nil || compiled.Policy == nil || rules.Validate(*compiled.Policy) != nil {
			writeJSONStatus(w, 409, map[string]string{"error": "插件返回了不兼容的规则，未保存"})
			return false
		}
		document["policy"], _ = json.Marshal(compiled.Policy)
		for _, v := range g.extensions.List(true) {
			if v.Manifest.ID == provider {
				document["provider_version"], _ = json.Marshal(v.Manifest.Version)
				break
			}
		}
		raw, _ = json.Marshal(document)
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	return true
}
