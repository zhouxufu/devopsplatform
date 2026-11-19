package deploy

// Evidence links refer to immutable reports, not to the current contents of a
// mutable test case. Payloads remain private to the originating reader. Linking
// a Bug must never grant access to another user's test report or log excerpt.
import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/apitest"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type bugCaseReference struct {
	ID       int64 `json:"id"`
	Revision int64 `json:"revision"`
}
type bugLogEvidence struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Source      string `json:"source"`
	Host        string `json:"host"`
	Timestamp   int64  `json:"timestamp"`
	TraceID     string `json:"trace_id"`
	Excerpt     string `json:"excerpt"`
}
type bugEvidence struct {
	TargetKey     string             `json:"target_key,omitempty"`
	ID            int64              `json:"id"`
	WorkItemID    int64              `json:"work_item_id"`
	Kind          string             `json:"kind"`
	Owner         string             `json:"owner"`
	CreatedAt     int64              `json:"created_at"`
	CanOpen       bool               `json:"can_open"`
	ApplicationID string             `json:"application_id,omitempty"`
	Service       string             `json:"service,omitempty"`
	Environment   string             `json:"environment,omitempty"`
	RunID         int64              `json:"run_id,omitempty"`
	Status        string             `json:"status,omitempty"`
	Cases         []bugCaseReference `json:"cases,omitempty"`
	SuiteID       int64              `json:"suite_id,omitempty"`
	SuiteRevision int64              `json:"suite_revision,omitempty"`
	FailedStep    int                `json:"failed_step,omitempty"`
	HTTPStatus    int                `json:"http_status,omitempty"`
	FailedChecks  int                `json:"failed_checks,omitempty"`
	Snapshot      string             `json:"snapshot,omitempty"`
	Log           *bugLogEvidence    `json:"log,omitempty"`
	Note          string             `json:"note,omitempty"`
}
type bugEvidenceInput struct {
	RequestKey string          `json:"request_key"`
	WorkItemID int64           `json:"work_item_id"`
	Title      string          `json:"title"`
	Note       string          `json:"note"`
	Kind       string          `json:"kind"`
	Service    string          `json:"service"`
	RunID      int64           `json:"run_id"`
	Log        *bugLogEvidence `json:"log,omitempty"`
}

var bugCredentialLine = regexp.MustCompile(`(?im)(authorization|proxy-authorization|cookie|set-cookie)\s*[:=][^\r\n]*`)
var bugCredentialPair = regexp.MustCompile(`(?i)(["']?(?:[a-z0-9_-]*(?:password|passwd|secret|token|api[_-]?key)[a-z0-9_-]*)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s&,;}\r\n]+)`)
var bugBearer = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/=-]+`)
var bugTrace = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,128}$`)

func bugSafeText(value string, limit int) string {
	// Redact complete input before truncation, including plain-text log formats.
	value = apitest.Redact(value, nil)
	value = bugCredentialLine.ReplaceAllString(value, "$1: [REDACTED]")
	value = bugCredentialPair.ReplaceAllString(value, "$1[REDACTED]")
	value = bugBearer.ReplaceAllString(value, "Bearer [REDACTED]")
	r := []rune(value)
	if len(r) > limit {
		return string(r[:limit]) + "…（已截断）"
	}
	return value
}
func bugEvidenceHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func bugTestEvidence(run *apiTestRun, app string) bugEvidence {
	v := bugEvidence{Kind: "test", Owner: run.Owner, ApplicationID: app, Service: run.Service, Environment: run.Environment, RunID: run.ID, Status: run.Status, SuiteID: run.SuiteID, SuiteRevision: run.SuiteRevision, CanOpen: true, TargetKey: run.Target.Key}
	seen := map[int64]bool{}
	for _, unit := range run.Units {
		c := unit.CaseSnapshot
		if c.ID > 0 && !seen[c.ID] {
			v.Cases = append(v.Cases, bugCaseReference{c.ID, c.Revision})
			seen[c.ID] = true
		}
	}
	if run.CaseID > 0 && !seen[run.CaseID] {
		v.Cases = append(v.Cases, bugCaseReference{run.CaseID, run.CaseRevision})
	}
	result := run.Result
	for i, step := range run.Steps {
		if step.Status == "failed" || step.Status == "unknown" {
			v.FailedStep = i + 1
			result = step.Result
			break
		}
	}
	v.HTTPStatus = result.Status
	for _, check := range result.Checks {
		if !check.Passed {
			v.FailedChecks++
		}
	}
	// No auth headers, request body, data rows or variable values are copied.
	// The report has already been redacted by the execution engine; redact again.
	v.Snapshot = bugSafeText(result.Body, 2048)
	if result.Body == "" && len(result.Events) > 0 {
		v.Snapshot = "最后接收的 SSE 事件（不代表失败原因）：\n" + bugSafeText(result.Events[len(result.Events)-1].Data, 2048)
	}
	if result.Error != "" {
		v.Snapshot = bugSafeText(result.Error, 400) + "\n" + v.Snapshot
	}
	v.Snapshot = bugSafeText(v.Snapshot, 2048)
	return v
}

func (s *Store) prepareBugEvidence(in bugEvidenceInput, actor string) (bugEvidence, error) {
	if actor == "" {
		return bugEvidence{}, fmt.Errorf("请先登录")
	}
	var v bugEvidence
	switch in.Kind {
	case "test":
		sv := s.GetService(in.Service)
		if sv == nil {
			return v, fmt.Errorf("应用不存在")
		}
		run, e := s.apiTests.load(sv.ApplicationID, actor, in.RunID)
		if e != nil {
			return v, e
		}
		if run.Status == "running" || run.Status == "queued" {
			return v, fmt.Errorf("请等待执行结束后关联结果")
		}
		v = bugTestEvidence(run, sv.ApplicationID)
	case "log":
		if in.Log == nil {
			return v, fmt.Errorf("请选择一条日志")
		}
		l := *in.Log
		for _, text := range []string{l.Service, l.Environment, l.Source, l.Host} {
			if len(text) > 191 || strings.ContainsAny(text, "\r\n\x00") {
				return v, fmt.Errorf("日志范围无效")
			}
		}
		if l.Service == "" || l.Environment == "" || l.Timestamp <= 0 || l.Timestamp > time.Now().Add(time.Hour).UnixMilli() || len(l.Excerpt) > 64<<10 || strings.TrimSpace(l.Excerpt) == "" {
			return v, fmt.Errorf("日志需要应用、环境、时间和摘要（最多 64 KiB）")
		}
		if l.TraceID != "" && !bugTrace.MatchString(l.TraceID) {
			return v, fmt.Errorf("日志 Trace ID 格式无效")
		}
		l.Excerpt = bugSafeText(l.Excerpt, 2048)
		v = bugEvidence{Kind: "log", Owner: actor, CanOpen: true, Service: l.Service, Environment: l.Environment, Log: &l}
		if sv := s.GetService(l.Service); sv != nil {
			v.ApplicationID = sv.ApplicationID
		}
	default:
		return v, fmt.Errorf("证据类型无效")
	}
	if len(in.Note) > 4000 {
		return v, fmt.Errorf("补充说明最多 1000 字")
	}
	v.Note = bugSafeText(in.Note, 1000)
	return v, nil
}

func (s *Store) saveBugEvidence(in bugEvidenceInput, actor string) (bugEvidence, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{16,64}$`).MatchString(in.RequestKey) {
		return bugEvidence{}, fmt.Errorf("缺少有效提交标识")
	}
	digest := bugEvidenceHash(in)
	// A retry returns the original association, even after the case/app changes.
	var existingID, existingWork int64
	var oldDigest string
	err := s.db.QueryRow(`SELECT id,work_item_id,request_digest FROM bug_evidence WHERE owner=? AND request_key=?`, actor, in.RequestKey).Scan(&existingID, &existingWork, &oldDigest)
	if err == nil {
		if oldDigest != digest {
			return bugEvidence{}, fmt.Errorf("提交标识已用于其他内容")
		}
		return bugEvidence{ID: existingID, WorkItemID: existingWork}, nil
	}
	if err != sql.ErrNoRows {
		return bugEvidence{}, err
	}
	v, err := s.prepareBugEvidence(in, actor)
	if err != nil {
		return v, err
	}
	ref := bugEvidenceHash(struct {
		Kind, App, Service string
		Run                int64
		Log                *bugLogEvidence
	}{v.Kind, v.ApplicationID, v.Service, v.RunID, v.Log})
	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	v.WorkItemID = in.WorkItemID
	if in.WorkItemID == 0 {
		if strings.TrimSpace(in.Title) == "" || len([]rune(in.Title)) > 200 {
			return v, fmt.Errorf("请填写 Bug 标题，最多 200 字")
		}
		item := WorkItem{Source: "manual", Kind: "bug", Title: strings.TrimSpace(in.Title), Owner: actor, Description: "来自" + map[string]string{"test": "接口测试", "log": "日志查询"}[v.Kind] + "的缺陷；复现证据见关联证据。", DescriptionFormat: "text"}
		if err = s.createWorkItemTx(tx, &item); err != nil {
			return v, err
		}
		v.WorkItemID = item.ID
	} else {
		var kind string
		if err = tx.QueryRow(`SELECT kind FROM work_items WHERE id=?`, in.WorkItemID).Scan(&kind); err != nil || kind != "bug" {
			return v, fmt.Errorf("请选择可访问的 Bug")
		}
	}
	// Repeatedly adding one execution to the same Bug is a no-op.
	if tx.QueryRow(`SELECT id FROM bug_evidence WHERE work_item_id=? AND owner=? AND reference_key=?`, v.WorkItemID, actor, ref).Scan(&existingID) == nil {
		v.ID = existingID
		return v, nil
	}
	v.CreatedAt = time.Now().Unix()
	raw, _ := json.Marshal(v)
	enc, err := s.encryptSecret(string(raw))
	if err != nil {
		return v, err
	}
	res, err := tx.Exec(`INSERT INTO bug_evidence(work_item_id,owner,request_key,request_digest,reference_key,kind,document,created_at) VALUES(?,?,?,?,?,?,?,?)`, v.WorkItemID, actor, in.RequestKey, digest, ref, v.Kind, enc, v.CreatedAt)
	if err != nil {
		return v, err
	}
	v.ID, err = res.LastInsertId()
	if err != nil {
		return v, err
	}
	if err = recordResearchRelation(tx, v.WorkItemID, actor, "participant"); err != nil {
		return v, err
	}
	_, err = tx.Exec(`INSERT INTO research_activity(work_item_id,actor,kind,content,created_at) VALUES(?,?,?,?,?)`, v.WorkItemID, actor, "comment", "关联了"+map[string]string{"test": "测试执行", "log": "日志"}[v.Kind]+"证据 #"+strconv.FormatInt(v.ID, 10)+"（原记录权限保持不变）", v.CreatedAt)
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}

func (s *Store) bugEvidenceList(id int64, actor string) ([]bugEvidence, error) {
	services := s.Services()
	rows, err := s.db.Query(`SELECT id,owner,kind,document,created_at FROM bug_evidence WHERE work_item_id=? ORDER BY id DESC LIMIT 200`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []bugEvidence{}
	for rows.Next() {
		var eid, at int64
		var owner, kind, enc string
		if err = rows.Scan(&eid, &owner, &kind, &enc, &at); err != nil {
			return nil, err
		}
		v := bugEvidence{ID: eid, WorkItemID: id, Owner: owner, Kind: kind, CreatedAt: at}
		if owner == actor {
			raw, e := s.decryptSecret(enc)
			if e != nil {
				return nil, e
			}
			if e = json.Unmarshal([]byte(raw), &v); e != nil {
				return nil, e
			}
			v.ID = eid
			v.CanOpen = true
			// Application identity, not a historical display name, resolves navigation.
			if v.ApplicationID != "" {
				v.Service = ""
				for _, sv := range services {
					if sv.ApplicationID == v.ApplicationID {
						v.Service = sv.Name
						break
					}
				}
			}
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func registerBugEvidenceRoutes(mux *http.ServeMux, s *Store) {
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<10))
		d.DisallowUnknownFields()
		if d.Decode(v) != nil {
			deliveryWriteError(w, 400, "证据参数格式错误或过大")
			return false
		}
		return true
	}
	for _, action := range []string{"preview", "link"} {
		action := action
		mux.HandleFunc("POST /api/bug-evidence/"+action, func(w http.ResponseWriter, r *http.Request) {
			var in bugEvidenceInput
			if !decode(w, r, &in) {
				return
			}
			var v bugEvidence
			var e error
			if action == "preview" {
				v, e = s.prepareBugEvidence(in, deliveryActor(r))
			} else {
				v, e = s.saveBugEvidence(in, deliveryActor(r))
			}
			if e != nil {
				deliveryWriteError(w, 400, e)
				return
			}
			deliveryJSON(w, v)
		})
	}
	mux.HandleFunc("GET /api/bug-evidence/bugs", func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{}
		search := strings.TrimSpace(r.URL.Query().Get("search"))
		if len(search) > 800 {
			deliveryWriteError(w, 400, "搜索词过长")
			return
		}
		id, _ := strconv.ParseInt(strings.TrimPrefix(search, "#"), 10, 64)
		rows, e := s.db.Query(`SELECT id,title,status,source FROM work_items WHERE kind='bug' AND (title LIKE ? OR external_id=? OR id=?) ORDER BY id DESC LIMIT 30`, "%"+search+"%", search, id)
		if e != nil {
			deliveryWriteError(w, 500, "Bug 列表读取失败")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var title, status, source string
			if e = rows.Scan(&id, &title, &status, &source); e != nil {
				deliveryWriteError(w, 500, "Bug 列表读取失败")
				return
			}
			out = append(out, map[string]any{"id": id, "title": title, "status": status, "source": source})
		}
		if rows.Err() != nil {
			deliveryWriteError(w, 500, "Bug 列表读取失败")
			return
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("GET /api/research/work-items/{id}/evidence", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if item := s.GetWorkItem(id); item == nil || item.Kind != "bug" {
			deliveryWriteError(w, 404, "Bug 不存在")
			return
		}
		out, e := s.bugEvidenceList(id, deliveryActor(r))
		if e != nil {
			deliveryWriteError(w, 500, "证据读取失败")
			return
		}
		deliveryJSON(w, out)
	})
	// Navigation resolves current case access, while preserving the historical link.
	mux.HandleFunc("GET /api/bug-evidence/{id}/regression", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var bug int64
		var enc string
		if s.db.QueryRow(`SELECT work_item_id,document FROM bug_evidence WHERE id=? AND owner=?`, id, deliveryActor(r)).Scan(&bug, &enc) != nil {
			deliveryWriteError(w, 404, "证据不存在或无权访问")
			return
		}
		raw, e := s.decryptSecret(enc)
		if e != nil {
			deliveryWriteError(w, 500, "证据读取失败")
			return
		}
		var evidence bugEvidence
		if json.Unmarshal([]byte(raw), &evidence) != nil {
			deliveryWriteError(w, 500, "证据读取失败")
			return
		}
		evidence.ID = id
		evidence.Service = ""
		for _, sv := range s.Services() {
			if sv.ApplicationID == evidence.ApplicationID {
				evidence.Service = sv.Name
				break
			}
		}
		items := []bugEvidence{evidence}
		for _, v := range items {
			if v.ID != id {
				continue
			}
			if v.Kind != "test" || v.Service == "" || len(v.Cases) == 0 {
				deliveryWriteError(w, 409, "该证据没有可复用的用例，请在接口测试中选择用例")
				return
			}
			q := url.Values{"service": {v.Service}, "environment": {v.Environment}, "bug": {strconv.FormatInt(bug, 10)}, "target": {v.TargetKey}}
			if v.SuiteID > 0 {
				if _, e := s.apiTestSuite(v.ApplicationID, deliveryActor(r), v.SuiteID, false); e != nil {
					deliveryWriteError(w, 409, "关联测试集已删除或无权访问，原证据仍保留")
					return
				}
				q.Set("suite", strconv.FormatInt(v.SuiteID, 10))
				q.Set("evidence_suite_revision", strconv.FormatInt(v.SuiteRevision, 10))
			} else if len(v.Cases) == 1 {
				c, e := s.apiTestCase(v.ApplicationID, deliveryActor(r), v.Cases[0].ID)
				if e != nil {
					deliveryWriteError(w, 409, "关联用例已删除或无权访问，原证据仍保留")
					return
				}
				q.Set("case", strconv.FormatInt(c.ID, 10))
				q.Set("evidence_revision", strconv.FormatInt(v.Cases[0].Revision, 10))
			} else {
				deliveryWriteError(w, 409, "请选择本次回归用例")
				return
			}
			deliveryJSON(w, map[string]string{"hash": "#/test-execution?" + q.Encode()})
			return
		}
		deliveryWriteError(w, 404, "证据不存在")
	})
}
