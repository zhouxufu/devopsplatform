package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"logplatform/pkg/apitest"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type apiTestEntry = apitest.Entry
type apiTestProfile = apitest.Profile
type apiTestTarget struct {
	CredentialReferencesValid bool   `json:"credential_references_valid"`
	AuthMode                  string `json:"auth_mode,omitempty"`
	LoginURL                  string `json:"login_url,omitempty"`
	Injection                 string `json:"injection,omitempty"`
	Configuration             string `json:"configuration"`
	Fingerprint               string `json:"fingerprint"`
	Key                       string `json:"key"`
	Label                     string `json:"label"`
	BaseURL                   string `json:"base_url"`
	Host                      string `json:"host,omitempty"`
	ArtifactID                int64  `json:"artifact_id,omitempty"`
	ArtifactDigest            string `json:"artifact_digest,omitempty"`
	DeployID                  int64  `json:"deploy_id,omitempty"`
}
type apiTestStep struct {
	Service       string               `json:"service,omitempty"`
	ApplicationID string               `json:"application_id,omitempty"`
	Kind          string               `json:"kind,omitempty"`
	Condition     *apiTestCondition    `json:"condition,omitempty"`
	ID            string               `json:"id,omitempty"`
	DocumentID    int64                `json:"document_id,omitempty"`
	OperationID   string               `json:"operation_id,omitempty"`
	Extractions   []apitest.Extraction `json:"extractions,omitempty"`
	Name          string               `json:"name"`
	Action        string               `json:"action"`
	Expected      string               `json:"expected"`
	Request       apitest.Request      `json:"request"`
}
type apiTestStepResult struct {
	Service        string                    `json:"service,omitempty"`
	ApplicationID  string                    `json:"application_id,omitempty"`
	Environment    string                    `json:"environment,omitempty"`
	Target         *apiTestTarget            `json:"target,omitempty"`
	DocumentID     int64                     `json:"document_id,omitempty"`
	DocumentDigest string                    `json:"document_digest,omitempty"`
	Kind           string                    `json:"kind,omitempty"`
	SkipReason     string                    `json:"skip_reason,omitempty"`
	BranchTaken    *bool                     `json:"branch_taken,omitempty"`
	ID             string                    `json:"id,omitempty"`
	Extractions    []apitest.ExtractionCheck `json:"extractions,omitempty"`
	Name           string                    `json:"name"`
	Action         string                    `json:"action"`
	Expected       string                    `json:"expected"`
	Request        apitest.Request           `json:"request"`
	Status         string                    `json:"status"`
	Result         apitest.Result            `json:"result"`
}
type apiTestCase struct {
	Scope             string           `json:"scope,omitempty"`
	Graph             *apiTestGraph    `json:"graph,omitempty"`
	DefinitionVersion int              `json:"definition_version,omitempty"`
	OperationID       string           `json:"operation_id,omitempty"`
	Owner             string           `json:"owner"`
	Visibility        string           `json:"visibility"`
	CanEdit           bool             `json:"can_edit"`
	DataRows          []apiTestDataRow `json:"data_rows,omitempty"`
	Module            string           `json:"module"`
	Priority          string           `json:"priority"`
	Preconditions     string           `json:"preconditions"`
	TestData          string           `json:"test_data"`
	Steps             []apiTestStep    `json:"steps"`
	ID                int64            `json:"id"`
	Name              string           `json:"name"`
	Revision          int64            `json:"revision"`
	DocumentID        int64            `json:"document_id"`
	Request           apitest.Request  `json:"request"`
}
type apiTestInput struct {
	VerifyAuth              bool   `json:"verify_auth,omitempty"`
	ExpectedVariablesDigest string `json:"expected_variables_digest,omitempty"`
	BugID                   int64  `json:"bug_id,omitempty"`
	expectedVariablesDigest string
	deliveryRunID           int64
	deliveryNodeID          string
	CaseRevisions           map[string]int64 `json:"case_revisions,omitempty"`
	SuiteID                 int64            `json:"suite_id"`
	SuiteRevision           int64            `json:"suite_revision"`
	TargetFingerprint       string           `json:"target_fingerprint"`
	RequestID               string           `json:"request_id"`
	Name                    string           `json:"name"`
	Environment             string           `json:"environment"`
	TargetKey               string           `json:"target_key"`
	DocumentID              int64            `json:"document_id"`
	CaseID                  int64            `json:"case_id"`
	CaseRevision            int64            `json:"case_revision"`
	Request                 apitest.Request  `json:"request"`
	ConfirmProtected        bool             `json:"confirm_protected"`
	Reason                  string           `json:"reason"`
}
type apiTestRun struct {
	Scenario           bool                   `json:"scenario,omitempty"`
	Targets            []apiScenarioRunTarget `json:"targets,omitempty"`
	VerifyAuth         bool                   `json:"verify_auth,omitempty"`
	BugID              int64                  `json:"bug_id,omitempty"`
	BugEvidenceError   string                 `json:"bug_evidence_error,omitempty"`
	Bindings           []apiTestCaseBinding   `json:"bindings,omitempty"`
	DeliveryRunID      int64                  `json:"delivery_run_id,omitempty"`
	DeliveryNodeID     string                 `json:"delivery_node_id,omitempty"`
	SuiteID            int64                  `json:"suite_id,omitempty"`
	SuiteRevision      int64                  `json:"suite_revision,omitempty"`
	Units              []apiTestUnit          `json:"units,omitempty"`
	DurationMS         int64                  `json:"duration_ms"`
	Source             string                 `json:"source"`
	CaseSnapshot       *apiTestCase           `json:"case_snapshot,omitempty"`
	Steps              []apiTestStepResult    `json:"steps,omitempty"`
	ID                 int64                  `json:"id"`
	Name               string                 `json:"name"`
	Service            string                 `json:"service"`
	Environment        string                 `json:"environment"`
	Owner              string                 `json:"owner"`
	Status             string                 `json:"status"`
	Target             apiTestTarget          `json:"target"`
	DocumentID         int64                  `json:"document_id"`
	DocumentArtifactID int64                  `json:"document_artifact_id,omitempty"`
	DocumentCommit     string                 `json:"document_commit,omitempty"`
	DocumentDigest     string                 `json:"document_digest,omitempty"`
	CaseID             int64                  `json:"case_id"`
	CaseRevision       int64                  `json:"case_revision"`
	Request            apitest.Request        `json:"request"`
	Method             string                 `json:"method"`
	Path               string                 `json:"path"`
	Mode               string                 `json:"mode"`
	Result             apitest.Result         `json:"result"`
	StartedAt          int64                  `json:"started_at"`
	EndedAt            int64                  `json:"ended_at,omitempty"`
}
type apiTestManager struct {
	historyMu sync.Mutex
	wg        sync.WaitGroup
	closing   bool
	st        *Store
	client    *http.Client
	mu        sync.Mutex
	jobs      map[int64]context.CancelFunc
	owners    map[string]int
}

func newAPITestManager(st *Store) *apiTestManager {
	m := &apiTestManager{st: st, client: apitest.Client(), jobs: map[int64]context.CancelFunc{}, owners: map[string]int{}}
	// Never resume/replay a request after the process disappeared.
	_, _ = st.db.Exec(`UPDATE api_test_runs SET status='unknown',updated_at=? WHERE status IN ('queued','running')`, time.Now().Unix())
	return m
}
func (s *Store) apiTestingProfile(app, env string) (apiTestProfile, error) {
	v := apiTestProfile{Enabled: true, Auth: apitest.Auth{Mode: "none", Header: "Authorization", Prefix: "Bearer "}}
	var enc string
	e := s.db.QueryRow(`SELECT document FROM api_test_profiles WHERE application_id=? AND environment=?`, app, env).Scan(&enc)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return v, nil
		}
		return v, e
	}
	raw, e := s.decryptSecret(enc)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal([]byte(raw), &v)
	return v, e
}
func (s *Store) saveAPITestingProfile(app, env string, v apiTestProfile) error {
	old, e := s.apiTestingProfile(app, env)
	if e != nil {
		return e
	}
	if old.Revision != v.Revision {
		return fmt.Errorf("配置已被修改，请重新加载")
	}
	if v.RegressionSuiteID < 0 || (v.RegressionSuiteID > 0) != (v.RegressionTargetKey != "") {
		return fmt.Errorf("请同时选择交付回归测试集和目标，或同时清空")
	}
	if v.RegressionSuiteID > 0 {
		suite, e := s.apiTestSuite(app, "", v.RegressionSuiteID, false)
		if e != nil || suite.Visibility != "team" {
			return fmt.Errorf("交付回归必须引用当前应用的团队测试集")
		}
	}
	if _, e = referenceTestProfile(v); e != nil {
		return e
	}
	if e = apitest.ValidateProfile(v); e != nil {
		return e
	}
	if v.BaseURL != "" {
		if _, e = apitest.Base(v.BaseURL); e != nil {
			return e
		}
	}
	if e = apitest.ValidateAuth(v.Auth); e != nil {
		return e
	}
	v.Revision++
	raw, _ := json.Marshal(v)
	enc, e := s.encryptSecret(string(raw))
	if e != nil {
		return e
	}
	if old.Revision == 0 {
		_, e = s.db.Exec(`INSERT INTO api_test_profiles(application_id,environment,document,revision) VALUES(?,?,?,?)`, app, env, enc, v.Revision)
		return e
	}
	res, e := s.db.Exec(`UPDATE api_test_profiles SET document=?,revision=? WHERE application_id=? AND environment=? AND revision=?`, enc, v.Revision, app, env, old.Revision)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("配置已变化")
	}
	return nil
}
func (s *Store) apiTestVariables(sv *Service, env *ServiceEnvironment) (map[string]string, error) {
	out := map[string]string{}
	if d := s.GetServiceDelivery(sv.Name); d != nil {
		for _, g := range d.VariableGroups {
			values := s.variableGroupValues(g)
			if values == nil {
				return nil, fmt.Errorf("应用绑定变量组不可用")
			}
			for k, v := range values {
				out[k] = v
			}
		}
	}
	values, e := s.decryptRuntimeEnvironment(env.runtimeEnvEncrypted)
	if e != nil {
		return nil, e
	}
	for k, v := range values {
		out[k] = v
	}
	return out, nil
}
func (s *Store) apiTestTargets(sv *Service, env *ServiceEnvironment, p apiTestProfile) []apiTestTarget {
	out := []apiTestTarget{}
	seen := map[string]bool{}
	appendTarget := func(key, label, base, host string) {
		if _, e := apitest.Base(base); e != nil || seen[key] {
			return
		}
		seen[key] = true
		t := apiTestTarget{Key: key, Label: label, BaseURL: base, Host: host}
		auth := p.Auth
		for _, entry := range p.Entries {
			if key == "endpoint:"+entry.Key {
				auth = entry.Auth
				break
			}
		}
		safeAuth, referenceErr := apitest.ReferenceAuth(auth)
		t.CredentialReferencesValid = referenceErr == nil
		auth = safeAuth
		t.AuthMode = auth.Mode
		t.Injection = auth.Injection
		if auth.Mode == "login" {
			authBase := base
			if auth.BaseURL != "" {
				authBase = auth.BaseURL
			}
			t.LoginURL, _ = apitest.Join(authBase, auth.Path)
		}
		query := `SELECT id,artifact_id,artifact_digest FROM deploys WHERE service=? AND environment=? AND status='success'`
		args := []any{sv.Name, env.Environment}
		if host != "" {
			query += " AND host=?"
			args = append(args, host)
		}
		query += " ORDER BY id DESC LIMIT 1"
		_ = s.db.QueryRow(query, args...).Scan(&t.DeployID, &t.ArtifactID, &t.ArtifactDigest)
		configuration, _ := json.Marshal([]any{sv.ApplicationID, env.Environment, env.Protected, p.Revision, key, base})
		cs := sha256.Sum256(configuration)
		t.Configuration = hex.EncodeToString(cs[:])
		identity, _ := json.Marshal([]any{sv.ApplicationID, env.Environment, env.Protected, p.Revision, key, base, t.ArtifactID, t.ArtifactDigest})
		digest := sha256.Sum256(identity)
		t.Fingerprint = hex.EncodeToString(digest[:])
		out = append(out, t)
	}
	if p.BaseURL != "" {
		appendTarget("entry", "环境访问入口", p.BaseURL, "")
	}
	for _, entry := range p.Entries {
		appendTarget("endpoint:"+entry.Key, entry.Label, strings.TrimSuffix(entry.BaseURL, "/")+entry.PathPrefix, "")
	}
	applied := env.Apply(sv)
	health, _ := url.Parse(strings.ReplaceAll(applied.HealthURL, "{host}", "127.0.0.1"))
	if p.BaseURL == "" && health != nil && health.Hostname() != "" && health.Hostname() != "localhost" && !net.ParseIP(health.Hostname()).IsLoopback() && !strings.Contains(applied.HealthURL, "{host}") {
		appendTarget("entry", "应用环境地址（健康检查来源）", health.Scheme+"://"+health.Host, "")
	}
	ports := []string{}
	addPort := func(p string) {
		n, e := strconv.Atoi(p)
		if e == nil && n > 0 && n < 65536 {
			for _, v := range ports {
				if v == p {
					return
				}
			}
			ports = append(ports, p)
		}
	}
	if applied.TargetType == "ssh-jar" || applied.TargetType == TargetJavaBundle {
		addPort(applied.DeployConfig["java_port"])
		if len(ports) == 0 && health != nil {
			addPort(health.Port())
		}
	}
	if applied.TargetType == "docker-host" {
		for _, mapping := range strings.Split(applied.DeployConfig["ports"], ",") {
			if strings.HasSuffix(mapping, "/udp") {
				continue
			}
			parts := strings.Split(strings.TrimSuffix(strings.TrimSpace(mapping), "/tcp"), ":")
			if len(parts) == 2 {
				addPort(parts[0])
			}
		}
		for _, p := range strings.Split(applied.DeployConfig["host_ports"], ",") {
			addPort(strings.TrimSpace(p))
		}
	}
	scheme := "http"
	if health != nil && health.Scheme == "https" {
		scheme = "https"
	}
	for _, name := range env.Hosts {
		h := s.GetHost(name)
		if h == nil {
			continue
		}
		address, e := normalizeSSHAddress(h.Addr)
		if e != nil {
			continue
		}
		host, _, e := net.SplitHostPort(address)
		if e != nil {
			continue
		}
		for _, port := range ports {
			appendTarget("host:"+name+":"+port, name+" · "+port, scheme+"://"+net.JoinHostPort(host, port), name)
		}
	}
	return out
}
func (s *Store) apiTestCase(app, owner string, id int64) (*apiTestCase, error) {
	var enc, who, visibility string
	var revision int64
	e := s.db.QueryRow(`SELECT c.document,c.revision,c.owner,COALESCE(a.visibility,'private') FROM api_test_cases c LEFT JOIN api_test_case_access a ON a.case_id=c.id WHERE c.application_id=? AND (c.owner=? OR a.visibility='team') AND c.id=? AND c.deleted=0`, app, owner, id).Scan(&enc, &revision, &who, &visibility)
	if e != nil {
		return nil, fmt.Errorf("用例不存在或无权访问")
	}
	plain, e := s.decryptSecret(enc)
	if e != nil {
		return nil, e
	}
	var v apiTestCase
	if e = json.Unmarshal([]byte(plain), &v); e != nil {
		return nil, e
	}
	v.ID = id
	v.Owner = who
	v.Visibility = visibility
	v.CanEdit = who == owner
	v.Revision = revision
	normalizeAPITestCase(&v)
	return &v, nil
}

// Legacy request-only cases become one executable step without changing their revision.
func normalizeAPITestCase(v *apiTestCase) {
	if v.Visibility == "" {
		v.Visibility = "private"
	}
	if v.Priority == "" {
		v.Priority = "P2"
	}
	if len(v.Steps) == 0 {
		v.Steps = []apiTestStep{{Name: "接口验证", Request: v.Request}}
	}
	if v.OperationID != "" && v.Steps[0].OperationID == "" {
		v.Steps[0].OperationID = v.OperationID
		v.Steps[0].DocumentID = v.DocumentID
	}
	used := map[string]bool{}
	for _, step := range v.Steps {
		if step.ID != "" {
			used[step.ID] = true
		}
	}
	for i := range v.Steps {
		if v.Steps[i].ID == "" {
			for n := i + 1; ; n++ {
				id := fmt.Sprintf("step-%d", n)
				if !used[id] {
					v.Steps[i].ID = id
					used[id] = true
					break
				}
			}
		}
	}
	v.Request = v.Steps[0].Request
}
func validateAPITestCase(v *apiTestCase) error {
	if v.Scope != "" && v.Scope != "scenario" {
		return fmt.Errorf("用例范围无效")
	}
	if v.DefinitionVersion < 0 || v.DefinitionVersion > 2 {
		return fmt.Errorf("不支持的用例定义版本")
	}
	if v.DefinitionVersion >= 1 {
		for _, step := range v.Steps {
			if step.ID == "" {
				return fmt.Errorf("节点 ID 缺失，请重新读取用例")
			}
		}
	}
	normalizeAPITestCase(v)
	if v.Visibility != "private" && v.Visibility != "team" {
		return fmt.Errorf("共享范围无效")
	}
	if len(v.Steps) > 10 {
		return fmt.Errorf("一个用例最多 10 个步骤")
	}
	if len(v.Module) > 200 || len(v.Preconditions) > 8000 || len(v.TestData) > 16000 {
		return fmt.Errorf("用例说明或测试数据过长")
	}
	if v.Priority != "P0" && v.Priority != "P1" && v.Priority != "P2" && v.Priority != "P3" {
		return fmt.Errorf("优先级无效")
	}
	nodeIDs := map[string]bool{}
	available := map[string]bool{}
	for i := range v.Steps {
		step := &v.Steps[i]
		if v.Scope != "scenario" && (step.Service != "" || step.ApplicationID != "") {
			return fmt.Errorf("跨应用节点请保存为业务场景用例")
		}
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`).MatchString(step.ID) || nodeIDs[step.ID] {
			return fmt.Errorf("步骤 %d 节点 ID 无效或重复", i+1)
		}
		nodeIDs[step.ID] = true
		if strings.TrimSpace(step.Name) == "" || len(step.Name) > 200 || len(step.Action) > 4000 || len(step.Expected) > 4000 {
			return fmt.Errorf("步骤 %d 请填写名称并缩短说明", i+1)
		}
		if testNodeKind(*step) != "http" {
			if v.Graph == nil || step.Condition != nil && testNodeKind(*step) != "condition" || len(step.Extractions) > 0 || step.DocumentID != 0 {
				return fmt.Errorf("控制节点配置无效")
			}
			step.Request = apitest.Request{}
			continue
		}
		if step.Condition != nil {
			return fmt.Errorf("接口节点不能携带条件配置")
		}
		if e := apitest.Validate(&step.Request); e != nil {
			return fmt.Errorf("步骤 %d：%w", i+1, e)
		}
		if e := apitest.ValidateCaseReferences(step.Request, nil, false); e != nil {
			return e
		}
		if e := apitest.ValidateStepReferences(step.Request, available); e != nil && v.Graph == nil {
			return fmt.Errorf("步骤 %d：%w", i+1, e)
		}
		if e := apitest.ValidateExtractions(step.Extractions, step.Request.Mode); e != nil {
			return fmt.Errorf("步骤 %d：%w", i+1, e)
		}
		for _, extraction := range step.Extractions {
			if available[extraction.Name] {
				return fmt.Errorf("步骤 %d：变量 %s 已由前面的步骤定义", i+1, extraction.Name)
			}
			available[extraction.Name] = true
		}
	}
	v.Request = v.Steps[0].Request
	v.DefinitionVersion = 1
	if v.Graph != nil {
		v.DefinitionVersion = 2
		if _, err := planAPITestGraph(v); err != nil {
			return err
		}
	}
	return validateAPITestData(v)
}
func apiTestRunSource(v *apiTestRun) string {
	if v.CaseID > 0 || v.SuiteID > 0 {
		return "case"
	}
	return "manual"
}
func apiTestResultStatus(result apitest.Result) string {
	if result.OutcomeUnknown {
		return "unknown"
	}
	if result.Error != "" || result.Status < 200 || result.Status >= 400 {
		return "failed"
	}
	for _, check := range result.Checks {
		if !check.Passed {
			return "failed"
		}
	}
	return "success"
}
func (s *Store) apiTestDocument(app string, id int64) (string, error) {
	if id == 0 {
		return "", nil
	}
	var digest string
	if s.db.QueryRow(`SELECT digest FROM api_document_versions WHERE application_id=? AND id=?`, app, id).Scan(&digest) != nil {
		return "", fmt.Errorf("文档不属于当前应用")
	}
	return digest, nil
}
func (m *apiTestManager) load(app, owner string, id int64) (*apiTestRun, error) {
	var enc, status string
	e := m.st.db.QueryRow(`SELECT document,status FROM api_test_runs WHERE id=? AND application_id=? AND owner=?`, id, app, owner).Scan(&enc, &status)
	if e != nil {
		return nil, fmt.Errorf("测试记录不存在或无权访问")
	}
	raw, e := m.st.decryptSecret(enc)
	if e != nil {
		return nil, e
	}
	var v apiTestRun
	e = json.Unmarshal([]byte(raw), &v)
	v.ID = id
	v.Status = status
	v.Source = apiTestRunSource(&v)
	if v.DurationMS == 0 {
		v.DurationMS = v.Result.DurationMS
	}
	if status == "unknown" {
		v.Result.OutcomeUnknown = true
		for i := range v.Units {
			if v.Units[i].Status == "running" {
				v.Units[i].Status = "unknown"
			}
			if v.Units[i].Status == "pending" {
				v.Units[i].Status = "skipped"
			}
		}
		for i := range v.Steps {
			if v.Steps[i].Status == "running" {
				v.Steps[i].Status = "unknown"
				v.Steps[i].Result.OutcomeUnknown = true
			}
			if v.Steps[i].Status == "pending" {
				v.Steps[i].Status = "skipped"
			}
		}
	}
	return &v, e
}
func (m *apiTestManager) persist(v apiTestRun) error {
	raw, _ := json.Marshal(v)
	enc, e := m.st.encryptSecret(string(raw))
	if e != nil {
		return e
	}
	_, e = m.st.db.Exec(`UPDATE api_test_runs SET document=?,status=?,updated_at=? WHERE id=?`, enc, v.Status, time.Now().Unix(), v.ID)
	if e == nil && v.BugID > 0 && v.EndedAt > 0 {
		_, linkErr := m.st.saveBugEvidence(bugEvidenceInput{Kind: "test", Service: v.Service, RunID: v.ID, WorkItemID: v.BugID, RequestKey: fmt.Sprintf("regression-run-%d", v.ID)}, v.Owner)
		if linkErr != nil {
			// Evidence failure must not change the outcome or replay the request.
			v.BugEvidenceError = "回归结果关联未完成，可在报告中重试关联"
			raw, _ = json.Marshal(v)
			enc, e = m.st.encryptSecret(string(raw))
			if e == nil {
				_, e = m.st.db.Exec(`UPDATE api_test_runs SET document=? WHERE id=?`, enc, v.ID)
			}
		}
	}
	return e
}
func (m *apiTestManager) start(sv *Service, owner string, admin bool, in apiTestInput) (*apiTestRun, error) {
	return m.startAuthorized(sv, owner, admin, in, false)
}

// workflowApproved is internal only; HTTP callers can never supply it.
func (m *apiTestManager) startAuthorized(sv *Service, owner string, admin bool, in apiTestInput, workflowApproved bool) (*apiTestRun, error) {
	if in.BugID != 0 {
		item := m.st.GetWorkItem(in.BugID)
		if item == nil || item.Kind != "bug" {
			return nil, fmt.Errorf("关联 Bug 不存在")
		}
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(in.RequestID) {
		return nil, fmt.Errorf("缺少有效请求标识")
	}
	if in.CaseID < 0 || in.SuiteID < 0 || in.CaseRevision < 0 || in.SuiteRevision < 0 {
		return nil, fmt.Errorf("用例或测试集标识无效")
	}
	if len(in.Name) > 200 || len(in.Reason) > 1000 {
		return nil, fmt.Errorf("名称或原因过长")
	}
	if in.CaseID == 0 && in.SuiteID == 0 {
		if e := apitest.Validate(&in.Request); e != nil {
			return nil, e
		}
	}
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	var previous int64
	var previousDigest string
	if m.st.db.QueryRow(`SELECT id,request_digest FROM api_test_runs WHERE application_id=? AND owner=? AND request_key=?`, sv.ApplicationID, owner, in.RequestID).Scan(&previous, &previousDigest) == nil {
		if digest != previousDigest {
			return nil, fmt.Errorf("请求标识已用于不同内容")
		}
		v, e := m.load(sv.ApplicationID, owner, previous)
		if e == nil && workflowApproved && (v.DeliveryRunID != in.deliveryRunID || v.DeliveryNodeID != in.deliveryNodeID) {
			return nil, fmt.Errorf("请求标识已被其他来源占用，请核对原执行记录")
		}
		return v, e
	}
	if m.closing {
		return nil, fmt.Errorf("服务正在停止")
	}
	if len(m.jobs) >= 4 || m.owners[owner] >= 1 {
		return nil, fmt.Errorf("已有测试正在执行，请先停止或等待完成")
	}
	env := m.st.GetServiceEnvironment(sv.Name, in.Environment)
	if env == nil {
		return nil, fmt.Errorf("应用未绑定此环境")
	}
	if env.Protected && !workflowApproved && (!admin || !in.ConfirmProtected || strings.TrimSpace(in.Reason) == "") {
		return nil, fmt.Errorf("受保护环境测试仅允许管理员确认目标并填写原因后执行")
	}
	p, e := m.st.apiTestingProfile(sv.ApplicationID, in.Environment)
	if e != nil {
		return nil, e
	}
	if !p.Enabled {
		return nil, fmt.Errorf("该环境已停用接口测试")
	}
	var target *apiTestTarget
	for _, t := range m.st.apiTestTargets(sv, env, p) {
		if t.Key == in.TargetKey {
			copy := t
			target = &copy
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("测试目标已变化，请重新选择环境入口或已绑定机器")
	}
	if in.TargetFingerprint != target.Fingerprint {
		return nil, fmt.Errorf("目标或认证配置已变化，请刷新目标后确认")
	}

	if strings.HasPrefix(target.Key, "endpoint:") {
		for _, entry := range p.Entries {
			if target.Key == "endpoint:"+entry.Key {
				p.Auth = entry.Auth
				break
			}
		}
	}
	if in.VerifyAuth && (in.CaseID != 0 || in.SuiteID != 0) {
		return nil, fmt.Errorf("登录验证不能同时执行用例或测试集")
	}
	var frozenCase *apiTestCase
	var planned []apiTestPlannedUnit
	var selectedCases []*apiTestCase
	var caseBindings []apiTestCaseBinding
	if in.SuiteID > 0 {
		if in.CaseID != 0 {
			return nil, fmt.Errorf("不能同时选择用例和测试集")
		}
		suite, e := m.st.apiTestSuite(sv.ApplicationID, owner, in.SuiteID, admin)
		if e != nil || suite.Revision != in.SuiteRevision {
			return nil, fmt.Errorf("测试集已变化，请重新加载")
		}
		caseBindings = append([]apiTestCaseBinding(nil), suite.Bindings...)
		in.Name = suite.Name
		in.DocumentID = 0
		for _, id := range suite.CaseIDs {
			c, e := m.st.apiTestCase(sv.ApplicationID, owner, id)
			if e != nil {
				return nil, e
			}
			if in.CaseRevisions[strconv.FormatInt(id, 10)] != c.Revision {
				return nil, fmt.Errorf("测试集中的用例已变化，请刷新后重新确认")
			}
			selectedCases = append(selectedCases, c)
		}
	} else if in.CaseID != 0 {
		c, e := m.st.apiTestCase(sv.ApplicationID, owner, in.CaseID)
		if e != nil || c.Revision != in.CaseRevision {
			return nil, fmt.Errorf("用例已变化，请重新加载后执行")
		}
		in.Name = c.Name
		in.DocumentID = c.DocumentID
		frozenCase = c
		selectedCases = append(selectedCases, c)
	}
	if e := validateAPITestBindings(caseBindings, selectedCases); e != nil {
		return nil, e
	}
	totalSteps := 0
	for _, c := range selectedCases {
		if e := m.st.validateCaseDocumentLinks(sv.ApplicationID, c); e != nil {
			return nil, e
		}
		if e := validateAPITestCase(c); e != nil {
			return nil, e
		}
		if _, e := m.st.apiTestDocument(sv.ApplicationID, c.DocumentID); e != nil {
			return nil, e
		}
		rows := c.DataRows
		if len(rows) == 0 {
			rows = []apiTestDataRow{{Name: "默认数据"}}
		}
		for _, row := range rows {
			data := map[string]any{}
			if len(c.DataRows) > 0 {
				var e error
				data, e = apiTestDataScope(row)
				if e != nil {
					return nil, e
				}
			}
			planned = append(planned, apiTestPlannedUnit{Case: *c, Data: data, RowName: row.Name})
			totalSteps += len(c.Steps)
		}
	}
	if len(planned) > 20 || totalSteps > 100 {
		return nil, fmt.Errorf("单次执行最多 20 个用例数据组合、100 个步骤，请拆分测试集")
	}
	if len(planned) > 0 {
		in.Request = planned[0].Case.Request
	}

	documentDigest, e := m.st.apiTestDocument(sv.ApplicationID, in.DocumentID)
	if e != nil {
		return nil, e
	}
	variables, e := m.st.apiTestVariables(sv, env)
	if e != nil {
		return nil, e
	}
	if in.ExpectedVariablesDigest != "" && in.ExpectedVariablesDigest != m.st.privateOperationDigest(variables) {
		return nil, fmt.Errorf("凭据变量已变化，请重新预览")
	}
	if workflowApproved && (in.deliveryRunID <= 0 || in.deliveryNodeID == "" || m.st.privateOperationDigest(variables) != in.expectedVariablesDigest) {
		return nil, fmt.Errorf("已审核变量或任务来源不匹配，请重新预览")
	}
	v := apiTestRun{VerifyAuth: in.VerifyAuth, BugID: in.BugID, DeliveryRunID: in.deliveryRunID, DeliveryNodeID: in.deliveryNodeID, Name: in.Name, Service: sv.Name, Environment: in.Environment, Owner: owner, Status: "queued", Target: *target, DocumentID: in.DocumentID, DocumentDigest: documentDigest, CaseID: in.CaseID, CaseRevision: in.CaseRevision, Method: in.Request.Method, Path: in.Request.Path, Mode: in.Request.Mode, StartedAt: time.Now().Unix(), Result: apitest.Result{Events: []apitest.Event{}, Checks: []apitest.Check{}}}
	v.Bindings = caseBindings
	v.Source = apiTestRunSource(&v)
	if in.VerifyAuth {
		in.Request.AuthenticationOnly = true
	}
	executionSteps := []apiTestStep{{Name: in.Name, Request: in.Request}}

	v.SuiteID = in.SuiteID
	v.SuiteRevision = in.SuiteRevision
	v.Source = apiTestRunSource(&v)
	if len(planned) > 0 {
		executionSteps = nil
		for _, unit := range planned {
			snapshot := unit.Case
			snapshot.DataRows = nil
			snapshot.Steps = append([]apiTestStep(nil), snapshot.Steps...)
			for i := range snapshot.Steps {
				snapshot.Steps[i].Request = apitest.SanitizeRequest(snapshot.Steps[i].Request, variables)
			}
			snapshot.Request = apitest.SanitizeRequest(snapshot.Request, variables)
			report := apiTestUnit{CaseSnapshot: snapshot, RowName: unit.RowName, Start: len(executionSteps), End: len(executionSteps) + len(snapshot.Steps), Status: "pending"}
			report.DocumentDigest, _ = m.st.apiTestDocument(sv.ApplicationID, snapshot.DocumentID)
			if snapshot.DocumentID > 0 {
				if e = m.st.db.QueryRow(`SELECT artifact_id,commit_ref FROM api_document_versions WHERE application_id=? AND id=?`, sv.ApplicationID, snapshot.DocumentID).Scan(&report.DocumentArtifactID, &report.DocumentCommit); e != nil {
					return nil, e
				}
			}
			v.Units = append(v.Units, report)
			executionSteps = append(executionSteps, unit.Case.Steps...)
			for _, step := range snapshot.Steps {
				v.Steps = append(v.Steps, apiTestStepResult{ID: step.ID, Kind: step.Kind, Name: step.Name, Action: step.Action, Expected: step.Expected, Request: step.Request, Status: "pending"})
			}
		}
		if frozenCase != nil {
			snapshot := v.Units[0].CaseSnapshot
			v.CaseSnapshot = &snapshot
		}
	}

	if in.DocumentID > 0 {
		if err := m.st.db.QueryRow(`SELECT artifact_id,commit_ref FROM api_document_versions WHERE application_id=? AND id=?`, sv.ApplicationID, in.DocumentID).Scan(&v.DocumentArtifactID, &v.DocumentCommit); err != nil {
			return nil, fmt.Errorf("文档身份读取失败")
		}
	}
	// Persist a redacted request snapshot; actual credentials live only for this execution.
	v.Path = apitest.SafeURL(in.Request.Path, variables)
	v.Request = apitest.SanitizeRequest(in.Request, variables)
	raw, _ = json.Marshal(v)
	enc, e := m.st.encryptSecret(string(raw))
	if e != nil {
		return nil, e
	}
	res, e := m.st.db.Exec(`INSERT INTO api_test_runs(application_id,service,owner,request_key,request_digest,document,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, sv.ApplicationID, sv.Name, owner, in.RequestID, digest, enc, v.Status, v.StartedAt, v.StartedAt)
	if e != nil {
		return nil, e
	}
	v.ID, _ = res.LastInsertId()
	if e = m.indexRun(&v, sv.ApplicationID); e != nil {
		v.Status = "failed"
		_ = m.persist(v)
		return nil, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	m.jobs[v.ID] = cancel
	m.owners[owner]++
	m.st.Audit(owner, "start", "api-test", fmt.Sprint(v.ID), map[string]any{"application_id": sv.ApplicationID, "environment": in.Environment, "method": in.Request.Method, "mode": in.Request.Mode, "reason": in.Reason})
	queuedRaw, _ := json.Marshal(v)
	var queued apiTestRun
	_ = json.Unmarshal(queuedRaw, &queued)
	m.wg.Add(1)
	go func(v apiTestRun) {
		defer m.wg.Done()
		defer func() { cancel(); m.mu.Lock(); delete(m.jobs, v.ID); m.owners[owner]--; m.mu.Unlock() }()
		v.Status = "running"
		if m.persist(v) != nil {
			return
		}
		for _, unit := range planned {
			if unit.Case.Graph != nil {
				m.runGraphCases(ctx, &v, planned, *target, p, variables, caseBindings)
				return
			}
		}
		v.Status = "success"
		scope := map[string]any{} // Private to this execution; never persisted.
		unitIndex := 0
		caseValues := map[string]any{} // Per execution only; no report or environment writes.
		for i, step := range executionSteps {
			if len(v.Units) > 0 {
				for unitIndex+1 < len(v.Units) && i >= v.Units[unitIndex].End {
					unitIndex++
				}
				if i == v.Units[unitIndex].Start {
					scope = map[string]any{}
					for key, value := range planned[unitIndex].Data {
						scope[key] = value
					}
					for _, binding := range caseBindings {
						if binding.TargetCaseID == planned[unitIndex].Case.ID {
							if value, ok := caseValues[binding.key()]; ok {
								scope["case."+binding.Name] = value
							}
						}
					}
					v.Units[unitIndex].Status = "running"
				}
			}
			if ctx.Err() != nil {
				v.Status = "canceled"
				break
			}
			if len(v.Steps) > 0 {
				v.Steps[i].Status = "running"
			}
			v.Request = apitest.SanitizeRequest(step.Request, variables)
			v.Method = step.Request.Method
			v.Mode = step.Request.Mode
			v.Path = apitest.SafeURL(step.Request.Path, variables)
			v.Result = apitest.Result{Events: []apitest.Event{}, Checks: []apitest.Check{}}
			persistProgress := func() {
				if len(v.Steps) > 0 {
					v.Steps[i].Result = v.Result
				}
				copy := v
				copy.Status = "running"
				_ = m.persist(copy)
			}
			persistProgress()
			stepCtx, stepCancel := context.WithTimeout(ctx, time.Duration(step.Request.Timeout)*time.Second)
			result, extraction := apitest.PerformScoped(stepCtx, m.client, target.BaseURL, step.Request, p.Auth, variables, scope, step.Extractions, func(event apitest.Event) { v.Result.Events = append(v.Result.Events, event); persistProgress() }, func(metadata apitest.Result) { v.Result = metadata; persistProgress() })
			stepCancel()
			if len(v.Steps) > 0 {
				v.Steps[i].Extractions = extraction.Checks
			}
			v.Result = result
			v.DurationMS += result.DurationMS
			v.Status = apiTestResultStatus(result)
			if ctx.Err() == context.Canceled {
				v.Status = "canceled"
				v.Result.StopReason = "user_stop"
				v.Result.Error = ""
				v.Result.OutcomeUnknown = step.Request.Mode != "sse" || v.Result.Status == 0
			}
			if len(v.Steps) > 0 {
				v.Steps[i].Status = v.Status
				v.Steps[i].Result = v.Result
			}
			if len(v.Units) > 0 {
				if v.Status != "success" || i+1 == v.Units[unitIndex].End {
					v.Units[unitIndex].Status = v.Status
				}
			}
			if v.Status != "success" {
				break
			}
			for name, value := range extraction.Values {
				scope[name] = value
			}
			if len(v.Units) > 0 && i+1 == v.Units[unitIndex].End {
				for _, binding := range caseBindings {
					if binding.SourceCaseID == planned[unitIndex].Case.ID && (binding.SourceRow == "" || binding.SourceRow == planned[unitIndex].RowName) {
						if value, ok := scope[binding.SourceName]; ok {
							caseValues[binding.key()] = value
						}
					}
				}
			}
		}
		for i := range v.Steps {
			if v.Steps[i].Status == "pending" {
				v.Steps[i].Status = "skipped"
			}
		}
		for i := range v.Units {
			if v.Units[i].Status == "pending" {
				v.Units[i].Status = "skipped"
			}
			if v.Units[i].Status == "running" {
				v.Units[i].Status = v.Status
			}
		}
		v.EndedAt = time.Now().Unix()
		_ = m.persist(v)
	}(v)
	return &queued, nil
}
func registerAPITestingRoutes(mux *http.ServeMux, st *Store, wj func(http.ResponseWriter, any), fail func(http.ResponseWriter, int, any), actor func(*http.Request) string) {
	st.apiTestOnce.Do(func() { st.apiTests = newAPITestManager(st) })
	m := st.apiTests
	admin := func(r *http.Request) bool { return r.Header.Get("X-User") == "" || r.Header.Get("X-Role") == "admin" }
	scope := func(w http.ResponseWriter, r *http.Request) *Service {
		sv := st.GetService(r.URL.Query().Get("service"))
		if sv == nil {
			fail(w, 400, "请选择已有应用")
		}
		return sv
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 384<<10))
		d.DisallowUnknownFields()
		if d.Decode(v) != nil {
			fail(w, 400, "请求格式错误或过大")
			return false
		}
		return true
	}
	registerAPITestLayoutRoutes(mux, st, wj, fail, actor, scope, decode)
	registerAPITestLayoutRoutes(mux, st, wj, fail, actor, scope, decode, "scenario-cases")
	registerAPIScenarioRoutes(mux, st, wj, fail, actor, admin, decode)
	registerAPITestMCPRoutes(mux, st, wj, fail, actor, admin, scope, decode)
	registerAPITestSuites(mux, st, wj, fail, actor, admin, scope, decode)
	registerAPITestImpactRoutes(mux, st, wj, fail, actor, admin, scope, decode)
	mux.HandleFunc("GET /api/api-testing/context", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		list := st.ServiceEnvironments(sv.Name)
		environments := []map[string]any{}
		for _, v := range list {
			environments = append(environments, map[string]any{"name": v.Environment, "display_name": v.DisplayName, "protected": v.Protected})
		}
		env := st.GetServiceEnvironment(sv.Name, r.URL.Query().Get("environment"))
		targets := []apiTestTarget{}
		keys := []string{}
		enabled := true
		authMode := "none"
		if env != nil {
			p, e := st.apiTestingProfile(sv.ApplicationID, env.Environment)
			if e != nil {
				fail(w, 500, "配置读取失败")
				return
			}
			targets = st.apiTestTargets(sv, env, p)
			enabled = p.Enabled
			authMode = p.Auth.Mode
			values, e := st.apiTestVariables(sv, env)
			if e != nil {
				fail(w, 409, "绑定变量不可用")
				return
			}
			for k := range values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
		}
		wj(w, map[string]any{"environments": environments, "targets": targets, "variables": keys, "enabled": enabled, "auth_mode": authMode})
	})
	mux.HandleFunc("GET /api/api-testing/profile", func(w http.ResponseWriter, r *http.Request) {
		if !admin(r) {
			fail(w, 403, "仅管理员可维护连接与认证")
			return
		}
		sv := scope(w, r)
		if sv == nil {
			return
		}
		env := r.URL.Query().Get("environment")
		if st.GetServiceEnvironment(sv.Name, env) == nil {
			fail(w, 400, "应用未绑定环境")
			return
		}
		v, e := st.apiTestingProfile(sv.ApplicationID, env)
		if e != nil {
			fail(w, 500, "配置读取失败")
			return
		}
		wj(w, v)
	})
	mux.HandleFunc("PUT /api/api-testing/profile", func(w http.ResponseWriter, r *http.Request) {
		if !admin(r) {
			fail(w, 403, "仅管理员可维护连接与认证")
			return
		}
		sv := scope(w, r)
		if sv == nil {
			return
		}
		env := r.URL.Query().Get("environment")
		if st.GetServiceEnvironment(sv.Name, env) == nil {
			fail(w, 400, "应用未绑定环境")
			return
		}
		var v apiTestProfile
		if !decode(w, r, &v) {
			return
		}
		if e := st.saveAPITestingProfile(sv.ApplicationID, env, v); e != nil {
			fail(w, 409, e)
			return
		}
		st.Audit(actor(r), "configure", "api-test-profile", sv.ApplicationID+"/"+env, map[string]any{"auth_mode": v.Auth.Mode, "enabled": v.Enabled})
		next, _ := st.apiTestingProfile(sv.ApplicationID, env)
		wj(w, next)
	})
	mux.HandleFunc("GET /api/api-testing/cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(w, 400, "用例编号无效")
			return
		}
		c, err := st.apiTestCase(sv.ApplicationID, actor(r), id)
		if err != nil {
			fail(w, 404, "用例不存在或无权访问")
			return
		}
		c.CanEdit = caseCanEdit(c, actor(r), admin(r))
		wj(w, c)
	})
	mux.HandleFunc("GET /api/api-testing/cases", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		rows, e := st.db.Query(`SELECT c.id FROM api_test_cases c LEFT JOIN api_test_case_access a ON a.case_id=c.id WHERE c.application_id=? AND (c.owner=? OR a.visibility='team') AND c.deleted=0 ORDER BY c.id DESC`, sv.ApplicationID, actor(r))
		if e != nil {
			fail(w, 500, e)
			return
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		out := []apiTestCase{}
		for _, id := range ids {
			if v, e := st.apiTestCase(sv.ApplicationID, actor(r), id); e == nil {
				v.CanEdit = caseCanEdit(v, actor(r), admin(r))
				out = append(out, *v)
			}
		}
		wj(w, out)
	})
	saveCase := func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		var v apiTestCase
		if !decode(w, r, &v) {
			return
		}
		if v.Scope != "" {
			fail(w, 400, "跨应用用例请保存为业务场景")
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		inputVersion := v.DefinitionVersion
		if len(strings.TrimSpace(v.Name)) == 0 || len(v.Name) > 200 {
			fail(w, 400, "请填写用例名称（最多 200 字节）")
			return
		}
		if e := validateAPITestCase(&v); e != nil {
			fail(w, 400, e)
			return
		}
		if _, e := st.apiTestDocument(sv.ApplicationID, v.DocumentID); e != nil {
			fail(w, 400, e)
			return
		}
		if e := st.validateCaseDocumentLinks(sv.ApplicationID, &v); e != nil {
			fail(w, 400, e)
			return
		}
		v.Owner = actor(r)
		if id > 0 {
			old, e := st.apiTestCase(sv.ApplicationID, actor(r), id)
			if e != nil || !caseCanEdit(old, actor(r), admin(r)) || old.Revision != v.Revision || !compatibleTestDefinition(old.DefinitionVersion, inputVersion, v.Graph) {
				fail(w, 409, "用例已变化或客户端定义版本过旧，请重新读取")
				return
			}
			v.Owner = old.Owner
		} else if v.Revision != 0 {
			fail(w, 409, "新用例修订号无效")
			return
		}
		v.ID = id
		v.CanEdit = true
		oldRevision := v.Revision
		v.Revision++
		raw, _ := json.Marshal(v)
		enc, e := st.encryptSecret(string(raw))
		if e != nil {
			fail(w, 500, "用例加密失败")
			return
		}
		tx, e := st.db.Begin()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer tx.Rollback()
		if id == 0 {
			res, e := tx.Exec(`INSERT INTO api_test_cases(application_id,owner,document,revision,deleted,updated_at) VALUES(?,?,?,?,0,?)`, sv.ApplicationID, actor(r), enc, v.Revision, time.Now().Unix())
			if e != nil {
				fail(w, 500, e)
				return
			}
			v.ID, _ = res.LastInsertId()
		} else {
			res, e := tx.Exec(`UPDATE api_test_cases SET document=?,revision=?,updated_at=? WHERE id=? AND application_id=? AND owner=? AND revision=? AND deleted=0`, enc, v.Revision, time.Now().Unix(), id, sv.ApplicationID, v.Owner, oldRevision)
			if e != nil {
				fail(w, 500, e)
				return
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				fail(w, 409, "用例已变化")
				return
			}
		}
		if _, e = tx.Exec(`INSERT INTO api_test_case_access(case_id,visibility) VALUES(?,?) ON CONFLICT(case_id) DO UPDATE SET visibility=excluded.visibility`, v.ID, v.Visibility); e != nil {
			fail(w, 500, e)
			return
		}
		if e = tx.Commit(); e != nil {
			fail(w, 500, e)
			return
		}
		wj(w, v)
	}
	mux.HandleFunc("POST /api/api-testing/cases", saveCase)
	mux.HandleFunc("PUT /api/api-testing/cases/{id}", saveCase)
	mux.HandleFunc("DELETE /api/api-testing/cases/{id}", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		c, e := st.apiTestCase(sv.ApplicationID, actor(r), id)
		if e != nil || !caseCanEdit(c, actor(r), admin(r)) {
			fail(w, 403, "无权删除用例")
			return
		}
		_, e = st.db.Exec(`UPDATE api_test_cases SET deleted=1 WHERE id=? AND application_id=? AND owner=? AND revision=?`, id, sv.ApplicationID, c.Owner, c.Revision)
		if e != nil {
			fail(w, 500, e)
			return
		}
		wj(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/api-testing/runs", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		var in apiTestInput
		if !decode(w, r, &in) {
			return
		}
		v, e := m.start(sv, actor(r), admin(r), in)
		if e != nil {
			fail(w, 409, e)
			return
		}
		wj(w, v)
	})
	mux.HandleFunc("GET /api/api-testing/runs", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		if e := m.history(w, r, sv.ApplicationID, actor(r), wj); e != nil {
			fail(w, 400, e)
		}
	})
	mux.HandleFunc("GET /api/api-testing/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v, e := m.load(sv.ApplicationID, actor(r), id)
		if e != nil {
			fail(w, 404, e)
			return
		}
		wj(w, v)
	})
	mux.HandleFunc("POST /api/api-testing/runs/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		sv := scope(w, r)
		if sv == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		v, e := m.load(sv.ApplicationID, actor(r), id)
		if e != nil {
			fail(w, 404, e)
			return
		}
		m.mu.Lock()
		cancel := m.jobs[id]
		if cancel != nil {
			cancel()
		}
		m.mu.Unlock()
		wj(w, v)
	})
}

func (m *apiTestManager) close() {
	m.mu.Lock()
	m.closing = true
	for _, cancel := range m.jobs {
		cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
