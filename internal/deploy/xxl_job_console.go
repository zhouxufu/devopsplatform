package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type xxlConsoleSession struct {
	client *http.Client
	root   string
}

var xxlConsoleWriteSlots = func() [32]chan struct{} {
	var slots [32]chan struct{}
	for index := range slots {
		slots[index] = make(chan struct{}, 1)
	}
	return slots
}()

type xxlConsolePage[T any] struct {
	Data  []T `json:"data"`
	Total int `json:"recordsFiltered"`
}

type xxlConsoleReply struct {
	Code    int             `json:"code"`
	Content json.RawMessage `json:"content"`
}

type xxlConsoleTask struct {
	XXLJobTask
	Author      string          `json:"author"`
	AlarmEmail  string          `json:"alarmEmail"`
	Misfire     string          `json:"misfireStrategy"`
	Route       string          `json:"executorRouteStrategy"`
	Parameters  string          `json:"executorParam"`
	Block       string          `json:"executorBlockStrategy"`
	Timeout     int             `json:"executorTimeout"`
	RetryCount  int             `json:"executorFailRetryCount"`
	ChildJobID  string          `json:"childJobId"`
	GlueUpdated json.RawMessage `json:"glueUpdatetime"`
}

type xxlConsoleRun struct {
	ID              json.Number     `json:"id"`
	GroupID         int             `json:"jobGroup"`
	TaskID          int             `json:"jobId"`
	ExecutorAddress string          `json:"executorAddress"`
	TriggerTime     json.RawMessage `json:"triggerTime"`
	TriggerCode     int             `json:"triggerCode"`
	TriggerMessage  string          `json:"triggerMsg"`
	HandleTime      json.RawMessage `json:"handleTime"`
	HandleCode      int             `json:"handleCode"`
	HandleMessage   string          `json:"handleMsg"`
}

func (run xxlConsoleRun) view() map[string]any {
	return map[string]any{
		"id": run.ID.String(), "executorAddress": run.ExecutorAddress, "triggerTime": run.TriggerTime,
		"triggerCode": run.TriggerCode, "triggerMsg": run.TriggerMessage, "handleTime": run.HandleTime,
		"handleCode": run.HandleCode, "handleMsg": run.HandleMessage,
	}
}

func (session xxlConsoleSession) post(ctx context.Context, route string, form url.Values, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, session.root+route, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("XXL-JOB 调度中心地址无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := session.client.Do(req)
	if err != nil {
		return fmt.Errorf("XXL-JOB 控制台未返回结果，请核对原任务")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusFound, http.StatusSeeOther, http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("XXL-JOB 控制台登录态或账号权限未通过验证")
	default:
		return fmt.Errorf("XXL-JOB 控制台未确认请求结果（HTTP %d）", resp.StatusCode)
	}
	const maxResponse = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse || json.Unmarshal(raw, output) != nil {
		return fmt.Errorf("XXL-JOB 控制台返回了无法读取或过大的响应")
	}
	return nil
}

func (session xxlConsoleSession) action(ctx context.Context, route string, form url.Values, result any) error {
	var reply xxlConsoleReply
	if err := session.post(ctx, route, form, &reply); err != nil {
		return err
	}
	if reply.Code != 200 {
		return fmt.Errorf("XXL-JOB 控制台未接受操作，请检查账号权限或任务配置")
	}
	if result != nil && json.Unmarshal(reply.Content, result) != nil {
		return fmt.Errorf("XXL-JOB 控制台返回的操作结果无法读取")
	}
	return nil
}

func xxlConsoleCall(ctx context.Context, connection *ResourceConnection, operation string, input map[string]any, output any) error {
	if connection.Kind != xxlJobKind || connection.Username == "" || connection.Password == "" {
		return fmt.Errorf("请配置 XXL-JOB 控制台专用账号和密码")
	}
	if operation == "receipt" {
		return fmt.Errorf("XXL-JOB 控制台不提供操作编号查询，请在原调度中心核对")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	session := xxlConsoleSession{
		root: strings.TrimRight(connection.Endpoint, "/"),
		client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	if err := session.action(ctx, "/login", url.Values{
		"userName": {connection.Username}, "password": {connection.Password},
	}, nil); err != nil {
		return fmt.Errorf("XXL-JOB 控制台登录失败，请检查地址和专用账号")
	}
	if operation == "create-group" || (operation == "apply" && !strings.HasPrefix(xxlConsoleText(input, "action"), "validate-")) {
		key := xxlDiscoveryEndpoint(session.root) + "/" + xxlConsoleText(input, "app_name")
		slot := xxlConsoleWriteSlots[crc32.ChecksumIEEE([]byte(key))%uint32(len(xxlConsoleWriteSlots))]
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		case <-ctx.Done():
			return fmt.Errorf("%w，等待同一调度中心任务配置写入超时", errXXLJobRejected)
		}
	}
	result, err := session.call(ctx, operation, input)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}

func xxlConsoleID(input map[string]any, key string) int64 {
	value, _ := strconv.ParseInt(fmt.Sprint(input[key]), 10, 64)
	return value
}

func xxlConsoleText(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return value
}

func (session xxlConsoleSession) groups(ctx context.Context, appName string) ([]XXLJobGroup, error) {
	result := []XXLJobGroup{}
	for offset := 0; offset < 5000; offset += 100 {
		var page xxlConsolePage[XXLJobGroup]
		if err := session.post(ctx, "/jobgroup/pageList", url.Values{
			"appname": {appName}, "start": {strconv.Itoa(offset)}, "length": {"100"},
		}, &page); err != nil {
			return nil, err
		}
		if page.Data == nil {
			return nil, fmt.Errorf("XXL-JOB 执行器列表格式不受支持")
		}
		for _, group := range page.Data {
			if group.ID > 0 && (appName == "" || group.AppName == appName) {
				result = append(result, group)
			}
		}
		if len(page.Data) == 0 || offset+len(page.Data) >= page.Total {
			return result, nil
		}
	}
	return nil, fmt.Errorf("XXL-JOB 执行器过多，请按应用筛选")
}

func (session xxlConsoleSession) call(ctx context.Context, operation string, input map[string]any) (any, error) {
	appName := xxlConsoleText(input, "app_name")
	if operation == "ping" {
		var page xxlConsolePage[XXLJobGroup]
		err := session.post(ctx, "/jobgroup/pageList", url.Values{"start": {"0"}, "length": {"1"}}, &page)
		if err == nil && page.Data == nil {
			err = fmt.Errorf("XXL-JOB 执行器列表格式不受支持")
		}
		return map[string]int{"version": 1}, err
	}
	groups, err := session.groups(ctx, appName)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "groups":
		return groups, nil
	case "create-group":
		return session.createGroup(ctx, appName, groups, input)
	}
	groupID := xxlConsoleID(input, "group_id")
	if len(groups) != 1 || appName == "" || int64(groups[0].ID) != groupID {
		return nil, fmt.Errorf("XXL-JOB 执行器身份发生变化，请刷新后核对")
	}
	tasks, err := session.groupTasks(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return session.taskOperation(ctx, operation, input, groupID, tasks)
}

func (session xxlConsoleSession) groupTasks(ctx context.Context, groupID int64) ([]xxlConsoleTask, error) {
	var tasks []xxlConsoleTask
	if err := session.action(ctx, "/joblog/getJobsByGroup", url.Values{"jobGroup": {strconv.FormatInt(groupID, 10)}}, &tasks); err != nil {
		return nil, err
	}
	if tasks == nil || slices.ContainsFunc(tasks, func(task xxlConsoleTask) bool { return task.ID <= 0 || int64(task.GroupID) != groupID }) {
		return nil, fmt.Errorf("XXL-JOB 任务列表格式或执行器归属无法核对")
	}
	return tasks, nil
}

func (session xxlConsoleSession) createGroup(ctx context.Context, appName string, groups []XXLJobGroup, input map[string]any) (any, error) {
	if !xxlAppName.MatchString(appName) || len(groups) != 0 {
		return nil, fmt.Errorf("该执行器 AppName 已存在或格式无效")
	}
	addresses, _ := input["addresses"].([]string)
	addressType := "0"
	if len(addresses) > 0 {
		addressType = "1"
	}
	if len(addresses) > 10 {
		return nil, fmt.Errorf("最多填写 10 个执行器地址")
	}
	for _, address := range addresses {
		if xxlDiscoveryEndpoint(address) == "" || strings.Contains(address, ",") {
			return nil, fmt.Errorf("执行器地址须为有效 HTTP(S) 地址")
		}
	}
	if err := session.action(ctx, "/jobgroup/save", url.Values{
		"appname": {appName}, "title": {xxlConsoleText(input, "title")},
		"addressType": {addressType}, "addressList": {strings.Join(addresses, ",")},
	}, nil); err != nil {
		return nil, err
	}
	groups, err := session.groups(ctx, appName)
	if err != nil || len(groups) != 1 {
		return nil, fmt.Errorf("无法核对新建执行器，请刷新原调度中心")
	}
	return groups[0], nil
}

func (task xxlConsoleTask) view() map[string]any {
	fields := map[string]any{
		"id": task.ID, "jobGroup": task.GroupID, "jobDesc": task.Title, "executorHandler": task.Handler,
		"scheduleType": task.ScheduleType, "scheduleConf": task.Schedule, "triggerStatus": task.TriggerStatus,
		"glueType": task.GlueType, "revision": digestJSON(task), "author": task.Author,
		"misfireStrategy": task.Misfire, "executorRouteStrategy": task.Route, "executorBlockStrategy": task.Block,
		"executorTimeout": task.Timeout, "executorFailRetryCount": task.RetryCount,
	}
	return fields
}

func xxlConsoleSlice(input map[string]any) (int64, int64) {
	offset, limit := xxlConsoleID(input, "offset"), xxlConsoleID(input, "limit")
	if limit == 0 {
		limit = 30
	}
	return max(0, min(offset, 2147483647)), max(1, min(limit, 100))
}

func (session xxlConsoleSession) taskOperation(ctx context.Context, operation string, input map[string]any, groupID int64, tasks []xxlConsoleTask) (any, error) {
	if operation == "tasks" || operation == "resolve" {
		query := strings.ToLower(xxlConsoleText(input, "query"))
		slices.SortFunc(tasks, func(left, right xxlConsoleTask) int { return right.ID - left.ID })
		items := []xxlConsoleTask{}
		for _, task := range tasks {
			if operation == "resolve" {
				if task.Title == xxlConsoleText(input, "target_title") && task.Handler == xxlConsoleText(input, "target_handler") {
					items = append(items, task)
				}
			} else if strings.Contains(strings.ToLower(task.Title), query) || strings.Contains(strings.ToLower(task.Handler), query) {
				items = append(items, task)
			}
		}
		if operation == "resolve" {
			views := make([]map[string]any, 0, len(items))
			for _, task := range items {
				views = append(views, task.view())
			}
			return views, nil
		}
		offset, limit := xxlConsoleSlice(input)
		start := min(int64(len(items)), offset)
		window := items[start:min(int64(len(items)), start+limit)]
		views := make([]map[string]any, 0, len(window))
		recent := xxlConsolePage[xxlConsoleRun]{}
		if len(window) > 0 {
			logCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			recent, _ = session.runPage(logCtx, xxlConsoleTask{XXLJobTask: XXLJobTask{GroupID: int(groupID)}}, 0, 100)
			cancel()
		}
		for _, task := range window {
			view := task.view()
			if index := slices.IndexFunc(recent.Data, func(run xxlConsoleRun) bool { return run.GroupID == task.GroupID && run.TaskID == task.ID }); index >= 0 {
				view["latestRun"] = recent.Data[index].view()
			} else {
				view["latestRunUnknown"] = true
			}
			views = append(views, view)
		}
		return map[string]any{"items": views, "total": len(items)}, nil
	}
	if operation == "apply" {
		return session.apply(ctx, input, groupID, tasks)
	}
	id := xxlConsoleID(input, "task_id")
	index := slices.IndexFunc(tasks, func(task xxlConsoleTask) bool { return int64(task.ID) == id })
	if index < 0 {
		return nil, fmt.Errorf("绑定的执行器下未找到该任务")
	}
	task := tasks[index]
	switch operation {
	case "task":
		return task.view(), nil
	case "trigger":
		err := session.action(ctx, "/jobinfo/trigger", url.Values{
			"id": {strconv.Itoa(task.ID)}, "executorParam": {xxlConsoleText(input, "parameters")},
		}, nil)
		return map[string]bool{"accepted": err == nil}, err
	case "runs", "run", "log":
		return session.logs(ctx, operation, task, input)
	default:
		return nil, fmt.Errorf("XXL-JOB 控制台不支持此操作")
	}
}

func (session xxlConsoleSession) runPage(ctx context.Context, task xxlConsoleTask, offset, limit int64) (xxlConsolePage[xxlConsoleRun], error) {
	var page xxlConsolePage[xxlConsoleRun]
	err := session.post(ctx, "/joblog/pageList", url.Values{
		"jobGroup": {strconv.Itoa(task.GroupID)}, "jobId": {strconv.Itoa(task.ID)}, "logStatus": {"0"},
		"start": {strconv.FormatInt(offset, 10)}, "length": {strconv.FormatInt(limit, 10)},
	}, &page)
	if err == nil && (page.Data == nil || slices.ContainsFunc(page.Data, func(run xxlConsoleRun) bool {
		return run.GroupID != task.GroupID || (task.ID > 0 && run.TaskID != task.ID)
	})) {
		err = fmt.Errorf("XXL-JOB 运行列表格式或任务归属无法核对")
	}
	if err != nil {
		return xxlConsolePage[xxlConsoleRun]{}, err
	}
	return page, err
}

func (session xxlConsoleSession) logs(ctx context.Context, operation string, task xxlConsoleTask, input map[string]any) (any, error) {
	offset, limit := xxlConsoleSlice(input)
	if operation == "runs" {
		page, err := session.runPage(ctx, task, offset, limit)
		items := make([]map[string]any, 0, len(page.Data))
		for _, run := range page.Data {
			items = append(items, run.view())
		}
		return map[string]any{"items": items, "total": page.Total}, err
	}
	runID := xxlConsoleID(input, "run_id")
	if runID <= 0 {
		return nil, fmt.Errorf("请指定运行编号")
	}
	id := strconv.FormatInt(runID, 10)
	find := func(start int64) (xxlConsoleRun, bool, int, error) {
		page, err := session.runPage(ctx, task, start, 100)
		if err != nil {
			return xxlConsoleRun{}, false, 0, err
		}
		for _, run := range page.Data {
			if run.ID.String() == id {
				return run, true, 0, nil
			}
		}
		if len(page.Data) == 0 || start+int64(len(page.Data)) >= int64(page.Total) {
			return xxlConsoleRun{}, false, 0, nil
		}
		return xxlConsoleRun{}, false, len(page.Data), nil
	}
	view := func(run xxlConsoleRun) (any, error) {
		if operation == "run" {
			return run.view(), nil
		}
		return session.logChunk(ctx, run, xxlConsoleID(input, "from_line"))
	}
	if offset > 0 {
		run, found, _, err := find(offset)
		if err != nil {
			return nil, err
		}
		if found {
			return view(run)
		}
	}
	for start := int64(0); start <= 2147483647; {
		run, found, count, err := find(start)
		if err != nil {
			return nil, err
		}
		if found {
			return view(run)
		}
		if count == 0 {
			break
		}
		start += int64(count)
	}
	return nil, fmt.Errorf("在该任务下未找到指定运行记录")
}

func (session xxlConsoleSession) logChunk(ctx context.Context, run xxlConsoleRun, from int64) (any, error) {
	millis := xxlConsoleTriggerMillis(run.TriggerTime)
	if millis <= 0 || run.TriggerCode != 200 || xxlDiscoveryEndpoint(run.ExecutorAddress) == "" {
		return nil, fmt.Errorf("该次运行尚无可读取的执行器日志")
	}
	var result struct {
		FromLineNum int    `json:"fromLineNum"`
		ToLineNum   int    `json:"toLineNum"`
		LogContent  string `json:"logContent"`
		End         bool   `json:"end"`
	}
	err := session.action(ctx, "/joblog/logDetailCat", url.Values{
		"executorAddress": {run.ExecutorAddress}, "logId": {run.ID.String()},
		"triggerTime": {strconv.FormatInt(millis, 10)}, "fromLineNum": {strconv.FormatInt(max(1, min(from, 2147483647)), 10)},
	}, &result)
	return result, err
}

func xxlConsoleTriggerMillis(raw json.RawMessage) int64 {
	var millis int64
	if json.Unmarshal(raw, &millis) == nil {
		return millis
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z0700"} {
			if timestamp, err := time.Parse(layout, value); err == nil {
				return timestamp.UnixMilli()
			}
		}
	}
	return 0
}

func (session xxlConsoleSession) apply(ctx context.Context, input map[string]any, groupID int64, tasks []xxlConsoleTask) (any, error) {
	action := xxlConsoleText(input, "action")
	preview := strings.HasPrefix(action, "validate-")
	action = strings.TrimPrefix(action, "validate-")
	var task xxlConsoleTask
	var route string
	if action == "create" {
		task = xxlConsoleTask{XXLJobTask: XXLJobTask{GroupID: int(groupID), GlueType: "BEAN", ScheduleType: "CRON"},
			Misfire: "DO_NOTHING", Route: "FIRST", Block: "SERIAL_EXECUTION"}
		route = "/jobinfo/add"
	} else {
		id := xxlConsoleID(input, "task_id")
		index := slices.IndexFunc(tasks, func(item xxlConsoleTask) bool { return int64(item.ID) == id })
		if index < 0 || digestJSON(tasks[index]) != xxlConsoleText(input, "revision") {
			return nil, fmt.Errorf("%w，请重新核对目标任务", errXXLJobRejected)
		}
		task = tasks[index]
		switch action {
		case "update":
			route = "/jobinfo/update"
		case "enable":
			route, task.TriggerStatus = "/jobinfo/start", 1
		case "disable":
			route, task.TriggerStatus = "/jobinfo/stop", 0
		default:
			return nil, fmt.Errorf("%w，操作类型无效", errXXLJobRejected)
		}
	}
	updateFields := action == "create" || action == "update"
	if updateFields {
		if err := xxlConsolePatch(&task, input["config"]); err != nil {
			return nil, err
		}
		if slices.ContainsFunc(tasks, func(other xxlConsoleTask) bool { return other.ID != task.ID && other.Title == task.Title }) {
			return nil, fmt.Errorf("%w，同执行器下存在同名任务", errXXLJobRejected)
		}
	}
	if (updateFields || action == "enable") && task.ScheduleType != "NONE" {
		var times []string
		if err := session.action(ctx, "/jobinfo/nextTriggerTime", url.Values{
			"scheduleType": {task.ScheduleType}, "scheduleConf": {task.Schedule},
		}, &times); err != nil || len(times) == 0 {
			return nil, fmt.Errorf("%w，调度规则无法解析或没有下一次运行，请在调度中心核对", errXXLJobRejected)
		}
	} else if action == "enable" {
		return nil, fmt.Errorf("%w，请先配置调度规则", errXXLJobRejected)
	}
	if preview {
		return task.view(), nil
	}
	current, err := session.groupTasks(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("%w，写入前无法重新核对目标任务", errXXLJobRejected)
	}
	if action != "create" && !slices.ContainsFunc(current, func(item xxlConsoleTask) bool {
		return item.ID == task.ID && digestJSON(item) == xxlConsoleText(input, "revision")
	}) {
		return nil, fmt.Errorf("%w，任务配置已变化，请重新预检", errXXLJobRejected)
	}
	if updateFields && slices.ContainsFunc(current, func(item xxlConsoleTask) bool {
		return item.ID != task.ID && item.Title == task.Title
	}) {
		return nil, fmt.Errorf("%w，同执行器下存在同名任务", errXXLJobRejected)
	}
	form := url.Values{"id": {strconv.Itoa(task.ID)}}
	if updateFields {
		form = task.form()
	}
	var newID string
	var result any
	if action == "create" {
		result = &newID
	}
	if err := session.action(ctx, route, form, result); err != nil {
		return nil, err
	}
	if action == "create" {
		task.ID, _ = strconv.Atoi(newID)
		if task.ID < 1 {
			return nil, fmt.Errorf("XXL-JOB 控制台未返回新建任务编号")
		}
	}
	actual, err := session.groupTasks(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if action == "create" {
		index := slices.IndexFunc(actual, func(item xxlConsoleTask) bool { return item.ID == task.ID })
		if index >= 0 {
			task.GlueUpdated = actual[index].GlueUpdated
		}
	}
	index := slices.IndexFunc(actual, func(item xxlConsoleTask) bool {
		return item.ID == task.ID && item.GroupID == task.GroupID && digestJSON(item) == digestJSON(task)
	})
	if index < 0 {
		return nil, fmt.Errorf("XXL-JOB 写入后无法核对预期任务配置，请查看原任务")
	}
	return actual[index].view(), nil
}

func xxlConsolePatch(task *xxlConsoleTask, input any) error {
	config, ok := input.(map[string]json.RawMessage)
	if !ok || len(config) == 0 || task.GlueType != "BEAN" {
		return fmt.Errorf("%w，仅支持修改 BEAN 任务配置", errXXLJobRejected)
	}
	text := map[string]*string{
		"jobDesc": &task.Title, "author": &task.Author, "alarmEmail": &task.AlarmEmail,
		"scheduleType": &task.ScheduleType, "scheduleConf": &task.Schedule, "misfireStrategy": &task.Misfire,
		"executorRouteStrategy": &task.Route, "executorHandler": &task.Handler, "executorParam": &task.Parameters,
		"executorBlockStrategy": &task.Block,
	}
	number := map[string]*int{"executorTimeout": &task.Timeout, "executorFailRetryCount": &task.RetryCount}
	for key, value := range config {
		if field := text[key]; field != nil && json.Unmarshal(value, field) == nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		if field := number[key]; field != nil && json.Unmarshal(value, field) == nil && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) && *field >= 0 {
			continue
		}
		return fmt.Errorf("%w，包含无效的任务配置", errXXLJobRejected)
	}
	if strings.TrimSpace(task.Title) == "" || strings.TrimSpace(task.Handler) == "" || strings.TrimSpace(task.Author) == "" ||
		!slices.Contains([]string{"CRON", "FIX_RATE", "NONE"}, task.ScheduleType) ||
		!slices.Contains([]string{"DO_NOTHING", "FIRE_ONCE_NOW"}, task.Misfire) ||
		!slices.Contains([]string{"FIRST", "LAST", "ROUND", "RANDOM", "CONSISTENT_HASH", "LEAST_FREQUENTLY_USED", "LEAST_RECENTLY_USED", "FAILOVER", "BUSYOVER", "SHARDING_BROADCAST"}, task.Route) ||
		!slices.Contains([]string{"SERIAL_EXECUTION", "DISCARD_LATER", "COVER_EARLY"}, task.Block) {
		return fmt.Errorf("%w，请检查任务标题、Handler、负责人和调度策略", errXXLJobRejected)
	}
	return nil
}

func (task xxlConsoleTask) form() url.Values {
	return url.Values{
		"id": {strconv.Itoa(task.ID)}, "jobGroup": {strconv.Itoa(task.GroupID)}, "jobDesc": {task.Title},
		"executorHandler": {task.Handler}, "scheduleType": {task.ScheduleType}, "scheduleConf": {task.Schedule},
		"glueType": {task.GlueType}, "author": {task.Author}, "alarmEmail": {task.AlarmEmail},
		"misfireStrategy": {task.Misfire}, "executorRouteStrategy": {task.Route}, "executorParam": {task.Parameters},
		"executorBlockStrategy": {task.Block}, "executorTimeout": {strconv.Itoa(task.Timeout)},
		"executorFailRetryCount": {strconv.Itoa(task.RetryCount)}, "childJobId": {task.ChildJobID},
	}
}
