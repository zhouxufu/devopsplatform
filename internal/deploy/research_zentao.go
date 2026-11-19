package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ResearchCredential struct {
	Account  string `json:"account"`
	Password string `json:"password"`
}
type ResearchAction struct {
	KeepFollowing  *bool            `json:"keep_following,omitempty"`
	OperationID    string           `json:"operation_id"`
	Action         string           `json:"action"`
	ExpectedDigest string           `json:"expected_digest"`
	Resolution     string           `json:"resolution"`
	ResolvedBuild  string           `json:"resolved_build"`
	AssignedTo     string           `json:"assigned_to"`
	DuplicateBug   string           `json:"duplicate_bug"`
	Comment        string           `json:"comment"`
	Title          string           `json:"title"`
	Description    string           `json:"description"`
	Attachments    []string         `json:"attachments"`
	Metadata       ResearchMetadata `json:"metadata"`
}
type ResearchRemote struct {
	Story          map[string]any    `json:"story,omitempty"`
	StageList      map[string]string `json:"stageList,omitempty"`
	Fields         map[string]any    `json:"fields,omitempty"`
	Bug            map[string]any    `json:"bug"`
	Actions        json.RawMessage   `json:"actions"`
	Users          map[string]string `json:"users"`
	Builds         map[string]string `json:"builds"`
	AssignedTo     string            `json:"assignedTo"`
	Digest         string            `json:"digest"`
	Resolutions    map[string]string `json:"resolutions,omitempty"`
	ResolutionList map[string]string `json:"resolutionList,omitempty"`
}
type ResearchOperation struct {
	ActivityID    int64          `json:"activity_id,omitempty"`
	ID            string         `json:"id"`
	State         string         `json:"state"`
	Message       string         `json:"message"`
	Request       ResearchAction `json:"request"`
	RemoteAccount string         `json:"remote_account"`
}

// Only pre-write conflicts are refreshable. An uncertain source write must
// still be reconciled by operation ID, never retried as a fresh submission.
type researchSourceConflict struct{ message string }

func (e *researchSourceConflict) Error() string { return e.message }

func (s *Store) researchConnection(w *WorkItem) (*ResourceConnection, error) {
	if w == nil || !strings.HasPrefix(w.Source, "zentao:") || (w.Kind != "bug" && w.Kind != "requirement") {
		return nil, fmt.Errorf("仅已关联的外部需求或 Bug 支持禅道操作")
	}
	if id, err := strconv.ParseInt(w.ExternalID, 10, 64); err != nil || id <= 0 {
		return nil, fmt.Errorf("外部 Bug 编号无效")
	}
	c := s.GetConnection(strings.TrimPrefix(w.Source, "zentao:"))
	if c == nil || c.Kind != "zentao" {
		return nil, fmt.Errorf("来源连接不存在")
	}
	return c, nil
}

func (s *Store) AuthorizeResearch(ctx context.Context, name, actor string, v ResearchCredential) error {
	if actor == "" || actor == "local" {
		return fmt.Errorf("禅道写操作须先启用并登录平台账号")
	}
	c := s.GetConnection(name)
	if c == nil || c.Kind != "zentao" {
		return fmt.Errorf("禅道连接不存在")
	}
	v.Account = strings.TrimSpace(v.Account)
	if v.Account == "" || len(v.Account) > 191 || len(v.Password) > 2048 {
		return fmt.Errorf("请填写本人禅道账号和密码")
	}
	// Explicit connection check can reuse only this actor's saved secret for
	// the same account and endpoint. Blank never means the public password.
	if v.Password == "" {
		_, saved, _, err := s.personalZentaoCredentials(name, actor)
		if err != nil || saved.Account != v.Account {
			return fmt.Errorf("首次验证或更换账号需要填写本人来源密码")
		}
		v.Password = saved.Password
	}
	c.Username, c.Password = v.Account, v.Password
	z, err := newZentaoLegacy(ctx, c)
	if err != nil {
		var auth *zentaoAuthenticationError
		if errors.As(err, &auth) {
			_, _, savedFingerprint, _ := s.personalZentaoCredentials(name, actor)
			entry := s.zentaoSession(zentaoSessionKey{actor, name})
			entry.mu.Lock()
			if savedFingerprint != "" {
				entry.fingerprint = savedFingerprint
			}
			if entry.client != nil {
				entry.client.authExpired.Store(true)
			}
			entry.requiresVerification = true
			entry.mu.Unlock()
		}
		return err
	}
	raw, _ := json.Marshal(v)
	secret, err := s.encryptSecret(string(raw))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO research_credentials(actor,connection_name,endpoint_digest,account,secret) VALUES(?,?,?,?,?) ON CONFLICT(actor,connection_name) DO UPDATE SET endpoint_digest=excluded.endpoint_digest,account=excluded.account,secret=excluded.secret`, actor, name, digestJSON(c.Endpoint), v.Account, secret)
	if err == nil {
		entry := s.zentaoSession(zentaoSessionKey{actor, name})
		entry.mu.Lock()
		if entry.client != nil {
			entry.client.authExpired.Store(true)
		}
		s.installZentaoSession(entry, z, digestJSON([]any{c.Endpoint, c.Config, secret}))
		entry.mu.Unlock()
	}
	return err
}

func (s *Store) researchClient(ctx context.Context, w *WorkItem, actor string) (*zentaoLegacy, string, error) {
	c, err := s.researchConnection(w)
	if err != nil {
		return nil, "", err
	}
	c, v, fingerprint, err := s.personalZentaoCredentials(c.Name, actor)
	if err != nil {
		return nil, "", err
	}
	c.Username, c.Password = v.Account, v.Password
	z, err := s.cachedZentao(ctx, c, actor, fingerprint)
	return z, v.Account, err
}

func (z *zentaoLegacy) researchRead(ctx context.Context, id, method string) (*ResearchRemote, error) {
	z.queryMu.Lock()
	defer z.queryMu.Unlock()
	// Other native Bug pages can replace their session's product/search setup.
	z.resetLiveSearch()
	v := &ResearchRemote{}
	if err := z.call(ctx, "bug", method, url.Values{"bugID": {id}}, nil, v); err != nil {
		return nil, err
	}
	if v.Bug == nil || fmt.Sprint(v.Bug["id"]) != id {
		return nil, fmt.Errorf("来源未返回可访问的 Bug，请检查项目权限")
	}
	// Native action controllers and view decorate the same stored Bug
	// differently (notably steps image sizing). Always lock against view, but
	// retain the action controller's fields, permitted users and build options.
	canonical := v
	if method != "view" {
		canonical = &ResearchRemote{}
		if err := z.call(ctx, "bug", "view", url.Values{"bugID": {id}}, nil, canonical); err != nil {
			return nil, err
		}
		if canonical.Bug == nil || fmt.Sprint(canonical.Bug["id"]) != id {
			return nil, fmt.Errorf("来源未返回可访问的 Bug，请检查项目权限")
		}
	}
	v.Digest = digestJSON(canonical.Bug)
	return v, nil
}

func validateResearchAction(v ResearchAction) error {
	if !attachmentIDRE.MatchString(v.OperationID) || len(v.Comment) > 32000 || len(v.Title) > 1000 || len(v.Description) > 1<<20 || len(v.Attachments) > 20 {
		return fmt.Errorf("请求编号或内容大小无效")
	}
	switch v.Action {
	case "develop", "resolve", "activate", "close", "assignTo", "comment", "edit":
	default:
		return fmt.Errorf("不支持的工作项操作")
	}
	if v.Action == "resolve" {
		if v.Resolution == "" || len(v.Resolution) > 64 || strings.ContainsAny(v.Resolution, "\x00\r\n") {
			return fmt.Errorf("解决方式无效")
		}
		if v.Resolution == "fixed" && v.ResolvedBuild == "" {
			return fmt.Errorf("已修复须选择禅道解决版本")
		}
		if v.Resolution == "duplicate" {
			if id, e := strconv.ParseInt(v.DuplicateBug, 10, 64); e != nil || id <= 0 {
				return fmt.Errorf("重复 Bug 编号必填")
			}
		}
		if v.Metadata.ReleaseRequired != nil || v.Metadata.UseRule {
			return validateResearchMetadata(v.Metadata)
		}
		return nil
	}
	if v.Action == "comment" && strings.TrimSpace(v.Comment) == "" && len(v.Attachments) == 0 {
		return fmt.Errorf("评论或附件不能为空")
	}
	if v.Action == "edit" && strings.TrimSpace(v.Title) == "" {
		return fmt.Errorf("标题必填")
	}
	return nil
}

// A single controller call preserves native actions/history/hooks. An ambiguous
// response is never retried: only a fresh view containing our action marker can
// establish success, including controller redirects after a committed write.
func (s *Store) writeResearchRemote(ctx context.Context, z *zentaoLegacy, w *WorkItem, v ResearchAction, before *ResearchRemote, actor string) error {
	z.queryMu.Lock()
	defer z.queryMu.Unlock()
	z.resetLiveSearch()
	if z.authExpired.Load() {
		return &zentaoAuthenticationError{"来源认证已失效，请重新验证本人来源账号；未提交写操作"}
	}
	method := v.Action
	if method == "comment" || method == "develop" {
		method = "edit"
	}
	form := url.Values{}
	if method == "edit" {
		for _, key := range []string{"title", "product", "branch", "module", "project", "execution", "story", "storyVersion", "severity", "pri", "type", "os", "browser", "steps", "task", "deadline", "assignedTo", "status", "confirmed", "resolution", "resolvedBy", "resolvedDate", "resolvedBuild", "closedBy", "closedDate", "duplicateBug", "mailto", "keywords"} {
			if value, ok := before.Bug[key]; ok {
				switch a := value.(type) {
				case []any:
					for _, x := range a {
						form.Add(key+"[]", fmt.Sprint(x))
					}
				case nil:
				default:
					form.Set(key, fmt.Sprint(a))
				}
			}
		}
		if v.Action == "edit" {
			form.Set("title", v.Title)
			form.Set("steps", v.Description)
		}
	}
	if w.Kind == "requirement" {
		var e error
		form, e = requirementEditForm(before, v.Action)
		if e != nil {
			return e
		}
	}
	if v.Action == "resolve" {
		form.Set("resolution", v.Resolution)
		form.Set("resolvedBuild", v.ResolvedBuild)
		if v.Resolution == "duplicate" {
			form.Set("duplicateBug", v.DuplicateBug)
		}
	}
	if v.AssignedTo != "" {
		form.Set("assignedTo", v.AssignedTo)
	}
	if v.Action == "activate" {
		form.Set("openedBuild[]", v.ResolvedBuild)
	}
	comment := v.Comment
	if v.Action == "resolve" && v.Metadata.ReleaseRequired != nil && !v.Metadata.UseRule {
		comment += "\n生产交付判定："
		if *v.Metadata.ReleaseRequired {
			comment += "独立生产交付候选"
		} else {
			comment += "不独立交付（可随主项）；原因：" + v.Metadata.NoReleaseReason
		}
	}
	form.Set("comment", strings.ReplaceAll(html.EscapeString(comment), "\n", "<br>")+"<br>[delivery-operation:"+v.OperationID+"]")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, values := range form {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				return err
			}
		}
	}
	seen := map[string]bool{}
	total := 0
	for _, id := range v.Attachments {
		if !attachmentIDRE.MatchString(id) || seen[id] {
			return fmt.Errorf("附件编号无效或重复")
		}
		seen[id] = true
		var name, owner string
		var bound int64
		if s.db.QueryRow(`SELECT name,owner,work_item_id FROM work_item_attachments WHERE id=?`, id).Scan(&name, &owner, &bound) != nil || owner != actor || (bound != 0 && bound != w.ID) {
			return fmt.Errorf("附件不属于当前用户或工作项")
		}
		data, err := os.ReadFile(filepath.Join(s.dataDir, "work-item-attachments", id))
		if err != nil {
			return fmt.Errorf("附件读取失败")
		}
		total += len(data)
		if total > 16<<20 {
			return fmt.Errorf("附件总大小超过 16MB")
		}
		part, err := writer.CreateFormFile("files[]", filepath.Base(name))
		if err != nil {
			return err
		}
		if _, err = part.Write(data); err != nil {
			return err
		}
	}
	writer.Close()
	module, args := "bug", url.Values{"bugID": {w.ExternalID}}
	if w.Kind == "requirement" {
		module, args = "story", url.Values{"storyID": {w.ExternalID}}
	}
	address, err := z.requestURL(module, method, args)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Referer", z.root+"/")
	if z.authExpired.Load() {
		return &zentaoAuthenticationError{"来源认证已失效，请重新验证本人来源账号；未提交写操作"}
	}
	resp, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("回写响应不确定，请核对结果；未自动重试")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	return nil
}

func researchMarkerFound(remote *ResearchRemote, id, actor, action string) bool {
	var raw any
	if json.Unmarshal(remote.Actions, &raw) != nil {
		return false
	}
	entries := []any{}
	switch v := raw.(type) {
	case []any:
		entries = v
	case map[string]any:
		for _, a := range v {
			entries = append(entries, a)
		}
	}
	expected := map[string]string{"resolve": "resolved", "activate": "activated", "close": "closed", "assignTo": "assigned", "comment": "edited", "edit": "edited", "develop": "edited"}[action]
	if id == "" || actor == "" || expected == "" {
		return false
	}
	for _, entry := range entries {
		a, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		actual := researchValue(a, "action")
		actionMatches := strings.EqualFold(actual, expected) || action == "comment" && strings.EqualFold(actual, "commented")
		if researchValue(a, "actor") == actor && actionMatches && strings.Contains(researchValue(a, "comment"), "[delivery-operation:"+id+"]") {
			return true
		}
	}
	return false
}

func (s *Store) ResearchOperation(id, actor string) (*ResearchOperation, error) {
	var raw, state string
	if s.db.QueryRow(`SELECT result,state FROM research_operations WHERE operation_id=? AND actor=?`, id, actor).Scan(&raw, &state) != nil {
		return nil, fmt.Errorf("操作不存在")
	}
	var out ResearchOperation
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil, fmt.Errorf("操作记录无效")
	}
	out.State = state
	return &out, nil
}

func (s *Store) completeResearchOperation(id int64, actor string, op *ResearchOperation, remote *ResearchRemote) error {
	if !researchMarkerFound(remote, op.ID, op.RemoteAccount, op.Request.Action) {
		return fmt.Errorf("尚未验证到本次操作历史，不重复提交；请重新核对或在禅道查看")
	}
	status := fmt.Sprint(remote.Bug["status"])
	if op.Request.Action == "develop" && (researchValue(remote.Story, "stage") != "developed" || status != "active") {
		return fmt.Errorf("尚未验证到需求开发完成状态，不自动重写")
	}
	if (op.Request.Action == "resolve" && status != "resolved") || (op.Request.Action == "activate" && status != "active") || (op.Request.Action == "close" && status != "closed") {
		return fmt.Errorf("来源状态与操作不一致，请核对")
	}
	item := s.GetWorkItem(id)
	if item == nil {
		return fmt.Errorf("工作项不存在")
	}
	fields := issueFields(s.GetConnection(strings.TrimPrefix(item.Source, "zentao:")), researchValue(remote.Bug, "severity"), researchValue(remote.Bug, "pri"), researchValue(remote.Bug, "resolution"), researchValue(remote.Bug, "closedDate"))
	fields.ProjectID = researchValue(remote.Bug, "project")
	fields.SourceProductID = researchValue(remote.Bug, "product")
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE work_items SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return err
	}
	if op.Request.Action == "resolve" && (op.Request.Metadata.ReleaseRequired != nil || op.Request.Metadata.UseRule) {
		if op.Request.Metadata.UseRule || !*op.Request.Metadata.ReleaseRequired {
			if err = checkResearchPlanned(tx, id); err != nil {
				return err
			}
		}
		if err = saveResearchMetadata(tx, id, op.Request.Metadata, actor); err != nil {
			return err
		}
	}
	if err = saveIssueFields(tx, id, fields); err != nil {
		return err
	}
	localStatus := WorkItemOpen
	if status == "closed" {
		localStatus = WorkItemClosed
	}
	description := researchValue(remote.Bug, "steps")
	if item.Kind == "requirement" {
		description = researchValue(remote.Story, "spec")
	}
	_, err = tx.Exec(`UPDATE work_items SET title=?,description=?,external_status=?,status=?,assigned_to=?,resolved_by=?,closed_by=?,updated_at=? WHERE id=?`, fmt.Sprint(remote.Bug["title"]), description, status, localStatus, researchValue(remote.Bug, "assignedTo"), researchValue(remote.Bug, "resolvedBy"), researchValue(remote.Bug, "closedBy"), time.Now().Unix(), id)
	if err != nil {
		return err
	}
	op.State, op.Message = "success", "禅道操作与历史已验证"
	raw, _ := json.Marshal(op)
	r, err := tx.Exec(`UPDATE research_operations SET state='success',result=? WHERE operation_id=? AND state<>'success'`, string(raw), op.ID)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fmt.Errorf("本次操作已处理，请重新读取")
	}
	activity, err := tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,?,?,?)`, id, actor, op.Request.Action, researchOperationActivityContent(op.Request), time.Now().Unix())
	if err != nil {
		return err
	}
	op.ActivityID, err = activity.LastInsertId()
	if err != nil {
		return err
	}
	raw, _ = json.Marshal(op)
	if _, err = tx.Exec(`UPDATE research_operations SET result=? WHERE operation_id=?`, string(raw), op.ID); err != nil {
		return err
	}
	if op.Request.Action != "assignTo" {
		if err = recordResearchRelation(tx, id, actor, "participant"); err != nil {
			return err
		}
	}
	if op.Request.KeepFollowing != nil {
		if *op.Request.KeepFollowing {
			err = recordResearchRelation(tx, id, actor, "follower")
		} else {
			_, err = tx.Exec(`DELETE FROM research_work_relations WHERE work_item_id=? AND actor=? AND kind='follower'`, id, actor)
		}
		if err != nil {
			return err
		}
	}
	for _, file := range op.Request.Attachments {
		if _, err = tx.Exec(`UPDATE work_item_attachments SET work_item_id=? WHERE id=? AND owner=?`, id, file, actor); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func researchValue(m map[string]any, key string) string {
	if m[key] == nil {
		return ""
	}
	return fmt.Sprint(m[key])
}

func (s *Store) ApplyResearchAction(ctx context.Context, id int64, actor string, v ResearchAction) (*ResearchOperation, error) {
	if err := validateResearchAction(v); err != nil {
		return nil, err
	}
	w := s.GetWorkItem(id)
	if w == nil {
		return nil, fmt.Errorf("工作项不存在")
	}
	if w.Kind == "requirement" && v.Action != "develop" && v.Action != "comment" {
		return nil, fmt.Errorf("外部需求支持开发完成及备注，验收／关闭由来源处理")
	}
	if w.Kind != "requirement" && v.Action == "develop" {
		return nil, fmt.Errorf("Bug 不能使用需求开发完成动作")
	}
	digest := digestJSON([]any{id, actor, v})
	var oldActor, oldDigest string
	if s.db.QueryRow(`SELECT actor,request_digest FROM research_operations WHERE operation_id=?`, v.OperationID).Scan(&oldActor, &oldDigest) == nil {
		if oldActor != actor || oldDigest != digest {
			return nil, fmt.Errorf("操作编号已用于其他请求")
		}
		return s.ResearchOperation(v.OperationID, actor)
	}
	z, account, err := s.researchClient(ctx, w, actor)
	if err != nil {
		return nil, err
	}
	before, err := z.researchReadItem(ctx, w, "view")
	if err != nil {
		return nil, err
	}
	if v.ExpectedDigest == "" || before.Digest != v.ExpectedDigest {
		return nil, &researchSourceConflict{"Bug 已被他人修改，请重新读取后提交"}
	}
	if v.Action == "resolve" && before.Bug["status"] != "active" {
		return nil, fmt.Errorf("仅激活状态的 Bug 可以解决")
	}
	if v.Action == "close" && before.Bug["status"] != "resolved" {
		return nil, fmt.Errorf("仅已解决 Bug 可以关闭")
	}
	if v.Action == "resolve" && w.MetadataRevision != v.Metadata.Revision {
		return nil, &researchSourceConflict{"上线意图已变化，请重新读取"}
	}
	if v.Action == "resolve" && (v.Metadata.UseRule || v.Metadata.ReleaseRequired != nil && !*v.Metadata.ReleaseRequired) {
		if err := checkResearchPlanned(s.db, id); err != nil {
			return nil, err
		}
	}
	method := v.Action
	if method == "comment" || method == "develop" {
		method = "edit"
	}
	options, err := z.researchReadItem(ctx, w, method)
	if err != nil {
		return nil, err
	}
	if w.Kind == "requirement" {
		if v.Action == "develop" && (researchValue(before.Story, "status") != "active" || requirementDevelopedLabel(options) == "" || (researchValue(before.Story, "stage") != "developing" && researchValue(before.Story, "stage") != "developed" && researchValue(before.Story, "stage") != "projected" && researchValue(before.Story, "stage") != "planned" && researchValue(before.Story, "stage") != "wait")) {
			return nil, fmt.Errorf("来源需求阶段不允许自动标记开发完成；请核对原状态与来源编辑权限")
		}
		if _, e := requirementEditForm(before, v.Action); e != nil {
			return nil, e
		}
	}
	if options.Digest != before.Digest {
		return nil, &researchSourceConflict{"读取操作表单期间 Bug 已变化，请重新读取"}
	}
	if v.Action == "resolve" {
		z.enrichResearchResolution(ctx, s.GetConnection(strings.TrimPrefix(w.Source, "zentao:")), options)
		if options.Resolutions[v.Resolution] == "" {
			return nil, fmt.Errorf("解决方案未在来源选项或管理员字典中定义，请先核对现场字典")
		}
	}
	if v.AssignedTo != "" && options.Users[v.AssignedTo] == "" {
		return nil, fmt.Errorf("所选接收人不在禅道允许列表")
	}
	if v.Action == "assignTo" && v.AssignedTo == "" {
		return nil, fmt.Errorf("请选择接收人")
	}
	if v.Action == "resolve" && v.ResolvedBuild != "" && v.ResolvedBuild != "trunk" && options.Builds[v.ResolvedBuild] == "" {
		return nil, fmt.Errorf("解决版本不在禅道允许列表")
	}
	if v.Action == "activate" && v.ResolvedBuild == "" {
		return nil, fmt.Errorf("激活 Bug 须选择影响版本")
	}
	op := &ResearchOperation{ID: v.OperationID, State: "executing", Message: "回写中；结果不确定时只核对，不重放写操作", Request: v, RemoteAccount: account}
	raw, _ := json.Marshal(op)
	if _, err = s.db.Exec(`INSERT INTO research_operations(operation_id,work_item_id,actor,request_digest,state,result,created_at) VALUES(?,?,?,?,'executing',?,?)`, v.OperationID, id, actor, digest, string(raw), time.Now().Unix()); err != nil {
		return nil, fmt.Errorf("操作已提交，请读取原操作结果")
	}
	writeErr := s.writeResearchRemote(ctx, z, w, v, before, actor)
	remote, readErr := z.researchReadItem(ctx, w, "view")
	if readErr == nil {
		err = s.completeResearchOperation(id, actor, op, remote)
	} else {
		err = readErr
	}
	if err != nil {
		op.State = "uncertain"
		op.Message = err.Error()
		if writeErr != nil {
			op.Message = writeErr.Error() + "；" + op.Message
		}
		raw, _ = json.Marshal(op)
		s.db.Exec(`UPDATE research_operations SET state='uncertain',result=? WHERE operation_id=? AND state<>'success'`, string(raw), op.ID)
	}
	s.Audit(actor, "source-operation", "work-item", fmt.Sprint(id), map[string]any{"operation": op.ID, "action": v.Action, "state": op.State, "source_account": account})
	return op, nil
}

func researchSourceIdentity(endpoint string) string {
	return fmt.Sprintf("zentao:%x", sha256.Sum256([]byte(strings.TrimRight(endpoint, "/"))))
}
