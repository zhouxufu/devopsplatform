package deploy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const xxlJobKind = "xxl_job"
const defaultXXLJobAddressKey = "xxl.job.admin.addresses"

var xxlAppName = regexp.MustCompile(`^[a-z][a-z0-9-]{3,63}$`)
var xxlJobPropertyKey = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,254}$`)
var errXXLJobRejected = errors.New("调度中心已拒绝且未写入本次任务变更")

type XXLJobGroup struct {
	ID           int      `json:"id"`
	AppName      string   `json:"appname"`
	Title        string   `json:"title"`
	AddressType  int      `json:"addressType"`
	RegistryList []string `json:"registryList"`
}

type XXLJobTask struct {
	ID            int    `json:"id"`
	GroupID       int    `json:"jobGroup"`
	Title         string `json:"jobDesc"`
	Handler       string `json:"executorHandler"`
	ScheduleType  string `json:"scheduleType"`
	Schedule      string `json:"scheduleConf"`
	TriggerStatus int    `json:"triggerStatus"`
	Revision      string `json:"revision"`
	GlueType      string `json:"glueType"`
}

type xxlJobBinding struct {
	Connection *ResourceConnection
	AppName    string
}

func validateXXLConnection(c *ResourceConnection) error {
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.User != nil || endpoint.ForceQuery || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.RawPath != "" {
		return fmt.Errorf("请填写 XXL-JOB 调度中心 HTTP(S) 根地址")
	}
	if !validResourceName(c.Config["environment"]) {
		return fmt.Errorf("请选择此 XXL-JOB 调度中心所属环境")
	}
	for key := range c.Config {
		if key != "environment" && key != "xxl_job_auth" {
			return fmt.Errorf("XXL-JOB 连接只支持环境和接入方式配置")
		}
	}
	switch c.Config["xxl_job_auth"] {
	case "", "adapter", "console":
	default:
		return fmt.Errorf("请选择有效的 XXL-JOB 接入方式")
	}
	return nil
}

func xxlConsoleConnection(c *ResourceConnection) bool {
	return c != nil && c.Config["xxl_job_auth"] == "console"
}

func xxlCall(ctx context.Context, c *ResourceConnection, operation string, input map[string]any, output any) error {
	if xxlConsoleConnection(c) {
		return xxlConsoleCall(ctx, c, operation, input, output)
	}
	if c == nil || c.Kind != xxlJobKind || len(c.Password) < 32 {
		return fmt.Errorf("请配置 XXL-JOB DevOps 接入令牌")
	}
	client := &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Endpoint, "/")+"/devops/v1/"+operation, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("XXL-JOB 请求配置无效")
	}
	req.Header.Set("Authorization", "Bearer "+c.Password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("XXL-JOB 未返回结果，请在原调度中心核对")
	}
	defer resp.Body.Close()
	if operation == "apply" && resp.StatusCode >= 400 && resp.StatusCode < 500 {
		var rejection struct {
			Status string `json:"status"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&rejection) == nil && rejection.Status == "rejected" {
			return fmt.Errorf("%w（HTTP %d），请刷新目标后重新预检", errXXLJobRejected, resp.StatusCode)
		}
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("XXL-JOB DevOps 接入令牌未通过认证")
	case http.StatusConflict:
		return fmt.Errorf("XXL-JOB 目标已存在或发生变化，请刷新后核对")
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return fmt.Errorf("XXL-JOB 拒绝本次配置，请检查调度规则和任务状态")
	case http.StatusNotFound:
		return fmt.Errorf("XXL-JOB 接入接口或指定目标不存在")
	default:
		return fmt.Errorf("XXL-JOB 未确认请求结果（HTTP %d），请核对原记录", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(output); err != nil {
		return fmt.Errorf("XXL-JOB 返回的回执无法读取，请核对原记录")
	}
	return nil
}

func validateXXLBinding(c *ResourceConnection, b ApplicationEnvironmentResource) error {
	if c == nil || c.Kind != xxlJobKind || c.Config["environment"] != b.Environment {
		return fmt.Errorf("XXL-JOB 必须选择同环境的调度中心")
	}
	if len(b.Config) != 1 || !xxlAppName.MatchString(b.Config["app_name"]) {
		return fmt.Errorf("请选择执行器 AppName")
	}
	return nil
}

func discoverXXLJobBinding(environment, addressKey string, properties map[string]string, connections []ResourceConnection) (resourceCandidate, bool) {
	explicitAddressKey := addressKey != ""
	if !explicitAddressKey {
		addressKey = defaultXXLJobAddressKey
	}
	addresses := strings.TrimSpace(resolveResourceProperty(properties[addressKey], properties))
	candidate := resourceCandidate{
		Environment: environment, Alias: "scheduler", Kind: xxlJobKind, Source: "源码与 Nacos · XXL-JOB",
		Supported: true, State: "unresolved", Message: "请核对调度中心地址和执行器 AppName",
	}
	var appName string
	for _, key := range []string{"appname", "app-name", "appName", "app_name"} {
		value := strings.TrimSpace(resolveResourceProperty(properties["xxl.job.executor."+key], properties))
		if value == "" {
			continue
		}
		if appName != "" && appName != value {
			candidate.Message = "执行器 AppName 存在不同取值，请核对应用配置"
			return candidate, true
		}
		appName = value
	}
	if addresses == "" && appName == "" && !explicitAddressKey {
		return resourceCandidate{}, false
	}
	if addresses == "" && explicitAddressKey {
		candidate.Message = "未在已读取的配置中找到调度中心地址，请核对配置键和来源"
		return candidate, true
	}
	if !xxlAppName.MatchString(appName) {
		return candidate, true
	}
	candidate.AppName = appName
	targets := map[string]bool{}
	for _, address := range strings.Split(addresses, ",") {
		key := xxlDiscoveryEndpoint(address)
		if key == "" {
			return candidate, true
		}
		targets[key] = true
	}
	matches := []string{}
	for _, connection := range connections {
		if connection.Kind == xxlJobKind && connection.Config["environment"] == environment && targets[xxlDiscoveryEndpoint(connection.Endpoint)] {
			matches = append(matches, connection.Name)
		}
	}
	switch len(matches) {
	case 0:
		candidate.State = "needs_connection"
		candidate.Message = "未匹配到本环境的调度中心，请到资源中心登记或核对地址"
	case 1:
		candidate.Connection = matches[0]
		candidate.State = "matched"
		candidate.Message = "已匹配本环境调度中心，可加入草稿并核验执行器"
		candidate.Applyable = true
	default:
		candidate.State = "ambiguous"
		candidate.Message = "有多条同环境连接匹配，请在资源配置中选择"
	}
	return candidate, true
}

func xxlDiscoveryEndpoint(raw string) string {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.User != nil || endpoint.ForceQuery || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.RawPath != "" ||
		strings.ContainsAny(raw, "{}@") {
		return ""
	}
	return endpoint.Scheme + "://" + strings.ToLower(endpoint.Host) + strings.TrimRight(endpoint.Path, "/")
}

func (s *Store) xxlJobBinding(service, environment, alias string) (xxlJobBinding, error) {
	if !s.hasResourceApplication(service) || s.GetEnvironment(environment) == nil {
		return xxlJobBinding{}, fmt.Errorf("应用或环境不存在")
	}
	resources := s.ApplicationResources(service)
	if resources.Error != "" {
		return xxlJobBinding{}, errors.New(resources.Error)
	}
	for _, requirement := range resources.Requirements {
		if requirement.Alias != alias || requirement.Kind != xxlJobKind {
			continue
		}
		for _, binding := range resources.Bindings {
			if binding.Alias == alias && binding.Environment == environment {
				connection := s.GetConnection(binding.Connection)
				if err := validateXXLBinding(connection, binding); err != nil {
					return xxlJobBinding{}, err
				}
				return xxlJobBinding{Connection: connection, AppName: binding.Config["app_name"]}, nil
			}
		}
	}
	return xxlJobBinding{}, fmt.Errorf("应用 %s 的资源 %s 未绑定 %s 环境的 XXL-JOB 执行器", service, alias, environment)
}

func (b xxlJobBinding) group(ctx context.Context) (XXLJobGroup, error) {
	var groups []XXLJobGroup
	if err := xxlCall(ctx, b.Connection, "groups", map[string]any{"app_name": b.AppName}, &groups); err != nil {
		return XXLJobGroup{}, err
	}
	var group XXLJobGroup
	for _, candidate := range groups {
		if candidate.AppName == b.AppName {
			if group.ID > 0 {
				return XXLJobGroup{}, fmt.Errorf("同一调度中心有多个执行器使用 AppName %s", b.AppName)
			}
			group = candidate
		}
	}
	if group.ID < 1 {
		return XXLJobGroup{}, fmt.Errorf("调度中心未找到执行器 %s", b.AppName)
	}
	return group, nil
}

func xxlGroupInput(group XXLJobGroup) map[string]any {
	return map[string]any{"group_id": group.ID, "app_name": group.AppName}
}

type xxlTriggerReceipt struct {
	Digest   string   `json:"digest"`
	RunIDs   []string `json:"run_ids,omitempty"`
	Accepted bool     `json:"accepted,omitempty"`
}

func (s *Store) xxlTrigger(ctx context.Context, binding xxlJobBinding, group XXLJobGroup, taskID int, actor, nonce, parameters string) (xxlTriggerReceipt, error) {
	if !validConnectionName(nonce) || len(nonce) < 16 || len(parameters) > 512 || taskID < 1 {
		return xxlTriggerReceipt{}, fmt.Errorf("请使用有效的一次执行请求和显式任务参数")
	}
	key := "xxl-job-trigger:" + s.privateResourceDigest([]any{actor, nonce})
	receipt := xxlTriggerReceipt{Digest: s.privateResourceDigest([]any{binding.Connection.Name, binding.Connection.Endpoint, group.ID, group.AppName, taskID, parameters})}
	initial, _ := json.Marshal(receipt)
	res, err := s.db.Exec("INSERT OR IGNORE INTO deploy_meta(key,value) VALUES(?,?)", key, string(initial))
	if err != nil {
		return receipt, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return receipt, err
	}
	if count == 0 {
		var raw string
		var previous xxlTriggerReceipt
		if err := s.db.QueryRow("SELECT value FROM deploy_meta WHERE key=?", key).Scan(&raw); err != nil {
			return receipt, err
		}
		if json.Unmarshal([]byte(raw), &previous) != nil || previous.Digest != receipt.Digest || (len(previous.RunIDs) == 0 && !previous.Accepted) {
			return receipt, fmt.Errorf("此请求已登记，调度结果尚未核对，请查看原任务的运行记录")
		}
		return previous, nil
	}
	input := xxlGroupInput(group)
	input["task_id"], input["parameters"] = taskID, parameters
	if xxlConsoleConnection(binding.Connection) {
		var result struct{ Accepted bool }
		if err := xxlCall(ctx, binding.Connection, "trigger", input, &result); err != nil {
			return receipt, err
		}
		receipt.Accepted = result.Accepted
	} else {
		if err := xxlCall(ctx, binding.Connection, "trigger", input, &receipt.RunIDs); err != nil {
			return receipt, err
		}
	}
	if len(receipt.RunIDs) == 0 && !receipt.Accepted {
		return receipt, fmt.Errorf("调度中心未返回运行编号，请核对原记录")
	}
	raw, _ := json.Marshal(receipt)
	_, err = s.db.Exec("UPDATE deploy_meta SET value=? WHERE key=? AND value=?", string(raw), key, string(initial))
	return receipt, err
}

func registerXXLJobRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("POST /api/delivery/runs/{id}/xxl-job-receipt", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		run := s.deliveryRunLedger(id)
		if run == nil {
			http.NotFound(w, r)
			return
		}
		if !deliveryManage(r, run.Owner) || !sameOrigin(r) {
			deliveryWriteError(w, 403, "仅交付发起人或管理员可核对回执")
			return
		}
		var input struct {
			NodeID   string `json:"node_id"`
			Digest   string `json:"digest"`
			Revision int64  `json:"revision"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input) != nil ||
			input.NodeID == "" || input.Digest != run.Digest || input.Revision != run.Revision || run.Status != "running" {
			deliveryWriteError(w, 409, "请刷新原执行单后核对回执")
			return
		}
		recovered, err := s.reconcileXXLJobStep(run, input.NodeID)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		if !recovered {
			deliveryWriteError(w, 409, "该步骤仍在执行或已有结果，请刷新原执行单")
			return
		}
		s.Audit(deliveryActor(r), "reconcile-xxl-job", "delivery-run", fmt.Sprint(id), map[string]any{"node": input.NodeID, "digest": run.Digest})
		deliveryJSON(w, map[string]bool{"recovered": true})
	})
	mux.HandleFunc("GET /api/xxl-job/bindings", func(w http.ResponseWriter, r *http.Request) {
		service, environment := r.URL.Query().Get("service"), r.URL.Query().Get("environment")
		if !s.hasResourceApplication(service) || s.GetEnvironment(environment) == nil {
			deliveryWriteError(w, 404, "应用或环境不存在")
			return
		}
		out := []ApplicationEnvironmentResource{}
		resources := s.ApplicationResources(service)
		if resources.Error != "" {
			deliveryWriteError(w, 409, resources.Error)
			return
		}
		for _, requirement := range resources.Requirements {
			if requirement.Kind != xxlJobKind {
				continue
			}
			for _, binding := range resources.Bindings {
				if binding.Environment == environment && binding.Alias == requirement.Alias {
					out = append(out, binding)
				}
			}
		}
		deliveryJSON(w, map[string]any{"items": out, "can_trigger": s.canTriggerXXLJob(service, environment)})
	})
	mux.HandleFunc("GET /api/xxl-job/connections/{name}/groups", func(w http.ResponseWriter, r *http.Request) {
		if !deliveryManage(r, "") {
			deliveryWriteError(w, 403, "仅管理员可管理执行器")
			return
		}
		c := s.GetConnection(r.PathValue("name"))
		if c == nil || c.Kind != xxlJobKind || c.Config["environment"] != r.URL.Query().Get("environment") {
			deliveryWriteError(w, 404, "同环境的调度中心不存在")
			return
		}
		items := []XXLJobGroup{}
		if err := xxlCall(r.Context(), c, "groups", map[string]any{}, &items); err != nil {
			deliveryWriteError(w, 502, err)
			return
		}
		deliveryJSON(w, items)
	})
	mux.HandleFunc("POST /api/xxl-job/connections/{name}/groups", func(w http.ResponseWriter, r *http.Request) {
		if !deliveryManage(r, "") || !sameOrigin(r) {
			deliveryWriteError(w, 403, "仅管理员可新建执行器")
			return
		}
		var input struct {
			Environment string   `json:"environment"`
			AppName     string   `json:"app_name"`
			Title       string   `json:"title"`
			Addresses   []string `json:"addresses"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input) != nil {
			deliveryWriteError(w, 400, "执行器信息无效")
			return
		}
		input.Title = strings.TrimSpace(input.Title)
		if !xxlAppName.MatchString(input.AppName) || input.Title == "" || utf8.RuneCountInString(input.Title) > 12 ||
			strings.ContainsAny(input.Title, "<>") {
			deliveryWriteError(w, 400, "请填写合法的执行器 AppName 和名称")
			return
		}
		c := s.GetConnection(r.PathValue("name"))
		if c == nil || c.Kind != xxlJobKind || c.Config["environment"] != input.Environment {
			deliveryWriteError(w, 404, "同环境的调度中心不存在")
			return
		}
		var group XXLJobGroup
		if err := xxlCall(r.Context(), c, "create-group", map[string]any{"app_name": input.AppName, "title": input.Title, "addresses": input.Addresses}, &group); err != nil {
			deliveryWriteError(w, 502, err)
			return
		}
		s.Audit(deliveryActor(r), "create", "xxl-job-group", fmt.Sprint(group.ID), map[string]any{"environment": input.Environment, "connection": c.Name, "app_name": group.AppName})
		deliveryJSON(w, group)
	})
	mux.HandleFunc("GET /api/xxl-job/{operation}", func(w http.ResponseWriter, r *http.Request) {
		operation := r.PathValue("operation")
		switch operation {
		case "tasks", "task", "runs", "run", "log", "group":
		default:
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		binding, err := s.xxlJobBinding(query.Get("service"), query.Get("environment"), query.Get("alias"))
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		group, err := binding.group(r.Context())
		if err != nil {
			deliveryWriteError(w, 502, err)
			return
		}
		if operation == "group" {
			deliveryJSON(w, group)
			return
		}
		input := xxlGroupInput(group)
		for _, key := range []string{"task_id", "run_id", "offset", "limit", "from_line"} {
			if text := query.Get(key); text != "" {
				value, err := strconv.ParseInt(text, 10, 64)
				if err != nil || value < 0 {
					deliveryWriteError(w, 400, "任务、运行或分页编号无效")
					return
				}
				input[key] = value
			}
		}
		input["query"] = query.Get("query")
		var out json.RawMessage
		if err := xxlCall(r.Context(), binding.Connection, operation, input, &out); err != nil {
			deliveryWriteError(w, 502, err)
			return
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("POST /api/xxl-job/trigger", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Service     string  `json:"service"`
			Environment string  `json:"environment"`
			Alias       string  `json:"alias"`
			TaskID      int     `json:"task_id"`
			Parameters  *string `json:"parameters"`
			Nonce       string  `json:"nonce"`
		}
		if !sameOrigin(r) {
			deliveryWriteError(w, 403, "请从定时任务工作台发起执行")
			return
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input) != nil || input.Parameters == nil {
			deliveryWriteError(w, 400, "请明确本次执行参数，留空表示传入空参数")
			return
		}
		if !s.canTriggerXXLJob(input.Service, input.Environment) {
			deliveryWriteError(w, 403, "此环境的定时任务不能从工作台直接触发")
			return
		}
		binding, err := s.xxlJobBinding(input.Service, input.Environment, input.Alias)
		if err != nil {
			deliveryWriteError(w, 400, err)
			return
		}
		group, err := binding.group(r.Context())
		if err == nil {
			var receipt xxlTriggerReceipt
			receipt, err = s.xxlTrigger(r.Context(), binding, group, input.TaskID, deliveryActor(r), input.Nonce, *input.Parameters)
			if err == nil {
				s.Audit(deliveryActor(r), "trigger", "xxl-job-task", fmt.Sprint(input.TaskID),
					map[string]any{"environment": input.Environment, "application": input.Service, "run_ids": receipt.RunIDs, "accepted": receipt.Accepted})
				deliveryJSON(w, map[string]any{"run_ids": receipt.RunIDs, "accepted": receipt.Accepted})
				return
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			err = fmt.Errorf("执行回执不可读取，请在原任务中核对")
		}
		deliveryWriteError(w, 502, err)
	})
}

func (s *Store) canTriggerXXLJob(service, environment string) bool {
	env := s.GetEnvironment(environment)
	target := s.GetServiceEnvironment(service, environment)
	return env != nil && !env.Protected && (env.Purpose == "development" || env.Purpose == "test") && (target == nil || !target.Protected)
}
