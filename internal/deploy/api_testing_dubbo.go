package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"logplatform/pkg/dubborpc"
	"net/url"
	"sort"
	"strings"
	"time"
)

func isTestRequest(s apiTestStep) bool {
	return testNodeKind(s) == "http" || testNodeKind(s) == "dubbo"
}
func (s *Store) dubboRegistry(service, environment string, entry apiTestEntry) (*dubborpc.Registry, error) {
	c, e := s.nacosBinding(service, environment, entry.RegistryAlias)
	if e != nil {
		return nil, e
	}
	if c.Config["nacos_auth"] == "token" {
		return nil, fmt.Errorf("Dubbo Nacos SDK 入口需要账号认证或无认证连接，不支持临时 Token")
	}
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Host == "" {
		return nil, fmt.Errorf("Nacos 注册中心地址无效")
	}
	if u.Scheme != "http" || u.Path != "" && u.Path != "/nacos" {
		return nil, fmt.Errorf("Dubbo Nacos SDK 需标准 http://主机:端口/nacos 连接，HTTPS 网关暂不支持")
	}
	group := entry.RegistryGroup
	if group == "" {
		group = "DEFAULT_GROUP"
	}
	return &dubborpc.Registry{Address: "nacos://" + u.Host, Namespace: c.Config["namespace"], Group: group, Username: c.Username, Password: c.Password}, nil
}
func (s *Store) resolveDubboTarget(ctx context.Context, service, environment string, p apiTestProfile, target apiTestTarget, r apitest.Request) (apiTestTarget, error) {
	if r.Mode != "dubbo" {
		if target.Protocol == "dubbo" {
			return target, fmt.Errorf("HTTP 节点需要 HTTP 入口")
		}
		return target, nil
	}
	if e := apitest.Validate(&r); e != nil {
		return target, e
	}
	if !dubborpc.Available() {
		return target, fmt.Errorf("Dubbo 执行器未安装，请配置 DEVOPS_DUBBO_EXECUTOR_JAR")
	}
	var chosen *apiTestEntry
	for _, entry := range p.Entries {
		if entry.Kind == "dubbo" && target.Key == "endpoint:"+entry.Key {
			copy := entry
			chosen = &copy
			break
		}
	}
	if chosen == nil {
		return target, fmt.Errorf("请选择 Dubbo 入口")
	}
	endpoint := chosen.BaseURL
	if chosen.RegistryAlias != "" {
		registry, e := s.dubboRegistry(service, environment, *chosen)
		if e != nil {
			return target, e
		}
		reply, e := dubborpc.Execute(ctx, dubborpc.Input{Action: "discover", RPC: *r.RPC, Registry: registry, TimeoutMS: 10000})
		if e != nil {
			return target, e
		}
		if len(reply.Providers) == 0 {
			return target, fmt.Errorf("Nacos 中未发现该接口 Group／Version 的可用提供者，请核对服务注册与元数据")
		}
		if len(reply.Providers) > 100 {
			return target, fmt.Errorf("提供者超过 100 个，请缩小服务范围")
		}
		sort.Strings(reply.Providers)
		endpoint = reply.Providers[0]
	}
	pinned, e := dubborpc.Resolve(ctx, endpoint)
	if e != nil {
		return target, e
	}
	target.BaseURL = pinned
	return target, nil
}

// Each node freezes its own transport target before the first business request.
// The primary selected target protects the profile revision; extra protocol
// entries come from that same application/environment profile, never the client.
func (s *Store) prepareProtocolTargets(service, environment string, p apiTestProfile, primary apiTestTarget, steps []apiTestStep, variables map[string]string) (map[string]apiScenarioRuntime, error) {
	out := map[string]apiScenarioRuntime{}
	resolved := map[string]apiTestTarget{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sv := s.GetService(service)
	env := s.GetServiceEnvironment(service, environment)
	for _, step := range steps {
		if !isTestRequest(step) {
			continue
		}
		target := primary
		auth := scenarioTargetAuth(p, target.Key)
		protocol := "http"
		if step.Request.Mode == "dubbo" {
			protocol = "dubbo"
		}
		if step.TargetKey != "" || protocol != target.Protocol && !(protocol == "http" && target.Protocol == "") {
			choices := []apiTestTarget{}
			for _, v := range s.apiTestTargets(sv, env, p) {
				if (v.Protocol == protocol || v.Protocol == "" && protocol == "http") && (step.TargetKey == "" || v.Key == step.TargetKey) {
					choices = append(choices, v)
				}
			}
			if len(choices) != 1 {
				return nil, fmt.Errorf("节点 %s 需要选择唯一的 %s 入口（可在节点指定入口标识）", step.Name, protocol)
			}
			target = choices[0]
			auth = scenarioTargetAuth(p, target.Key)
		}
		if !target.CredentialReferencesValid {
			return nil, fmt.Errorf("节点 %s 的入口配置或凭据不可用", step.Name)
		}
		var e error
		signature, _ := json.Marshal([]any{target.Fingerprint, step.Request.RPC})
		if cached, ok := resolved[string(signature)]; ok {
			target = cached
		} else {
			target, e = s.resolveDubboTarget(ctx, service, environment, p, target, step.Request)
			if e == nil {
				resolved[string(signature)] = target
			}
		}
		if e != nil {
			return nil, fmt.Errorf("节点 %s：%w", step.Name, e)
		}
		if e = apitest.ValidateVariableBindings(step.Request, auth, variables); e != nil {
			return nil, e
		}
		key := protocolRuntimeKey(step)
		if old, ok := out[key]; ok && old.Target.BaseURL != target.BaseURL {
			return nil, fmt.Errorf("相同节点目标不一致")
		}
		out[key] = apiScenarioRuntime{apiScenarioRunTarget: apiScenarioRunTarget{Service: service, ApplicationID: sv.ApplicationID, Environment: environment, Target: target}, Auth: auth, Variables: variables, Redactions: variables}
	}
	return out, nil
}
func protocolRuntimeKey(step apiTestStep) string {
	return "node:" + step.Service + ":" + step.ID + ":" + step.TargetKey + ":" + step.Request.Mode + ":" + step.Request.Path
}
func scenarioRuntimeFor(values map[string]apiScenarioRuntime, step apiTestStep) (apiScenarioRuntime, bool) {
	if v, ok := values[protocolRuntimeKey(step)]; ok {
		return v, true
	}
	v, ok := values[step.Service]
	return v, ok
}
func dubboEntryBase(entry apiTestEntry) string {
	if entry.RegistryAlias != "" {
		return "nacos://" + url.PathEscape(entry.RegistryAlias)
	}
	return strings.TrimSuffix(entry.BaseURL, "/")
}
