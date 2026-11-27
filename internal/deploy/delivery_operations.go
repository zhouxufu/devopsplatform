package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type VerificationSpec struct {
	Mode           string `json:"mode"` // manual | http
	URL            string `json:"url,omitempty"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}
type DeliveryScript struct {
	Target         string   `json:"target,omitempty"`
	Host           string   `json:"host"`
	Content        string   `json:"content"`
	Arguments      []string `json:"arguments,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	TargetDigest   string   `json:"target_digest,omitempty"` // server generated
}
type MonitorGate struct {
	URL            string  `json:"url"`
	WarmupSeconds  int     `json:"warmup_seconds"`
	WindowSeconds  int     `json:"window_seconds"`
	MinRequests    int64   `json:"min_requests"`
	MaxErrorRate   float64 `json:"max_error_rate"`
	MaxP95Millis   float64 `json:"max_p95_ms"`
	Consecutive    int     `json:"consecutive"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}
type NodeEvidence struct {
	Kind       string            `json:"kind"`
	Actor      string            `json:"actor,omitempty"`
	At         int64             `json:"at"`
	PlanDigest string            `json:"plan_digest"`
	Artifacts  map[string]string `json:"artifacts"`
	Message    string            `json:"message"`
}

func validateCheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" || u.RawQuery != "" || len(raw) > 2048 {
		return fmt.Errorf("检查地址须为无凭据、无查询参数的 HTTP(S) 地址")
	}
	if strings.ContainsAny(raw, "\r\n\x00") {
		return fmt.Errorf("检查地址无效")
	}
	return nil
}
func validateOperationNode(n DeliveryNode) error {
	if n.Kind == "verify" {
		v := n.Verification
		if v == nil {
			return fmt.Errorf("验证节点需要选择人工验证或 HTTP 验证")
		}
		if n.Service == "" {
			return fmt.Errorf("验证节点须选择一个范围内应用，以绑定准确制品")
		}
		if v.Mode == "manual" {
			return validateApprovalNode(n)
		}
		if v.Mode != "http" {
			return fmt.Errorf("验证方式无效")
		}
		if v.TimeoutSeconds < 1 || v.TimeoutSeconds > 30 || v.ExpectedStatus < 200 || v.ExpectedStatus > 299 {
			return fmt.Errorf("验证超时须为 1–30 秒，预期状态须为 2xx")
		}
		return validateCheckURL(v.URL)
	}
	if n.Kind == "script" {
		v := n.Script
		if v == nil || v.Host == "" || strings.TrimSpace(v.Content) == "" || len(v.Content) > 64<<10 || strings.ContainsRune(v.Content, 0) || v.TimeoutSeconds < 1 || v.TimeoutSeconds > 600 || len(v.Arguments) > 30 {
			return fmt.Errorf("脚本须选择执行主机、填写内容及 1–600 秒超时（最多 30 个参数）")
		}
		for _, a := range v.Arguments {
			if len(a) > 4096 || strings.ContainsRune(a, 0) {
				return fmt.Errorf("脚本参数无效")
			}
		}
	}
	if n.Monitor != nil {
		m := n.Monitor
		if n.Kind != "deploy" || n.Service == "" {
			return fmt.Errorf("自动观察须配置在指定单应用的发布节点上")
		}
		if err := validateCheckURL(m.URL); err != nil {
			return err
		}
		if m.WindowSeconds < 10 || m.WindowSeconds > 1800 || m.WarmupSeconds < 0 || m.WarmupSeconds > 600 || m.TimeoutSeconds < m.WarmupSeconds+m.WindowSeconds || m.TimeoutSeconds > 3600 || m.MinRequests < 1 || m.MaxErrorRate < 0 || m.MaxErrorRate > 1 || m.MaxP95Millis <= 0 || m.Consecutive < 1 || m.Consecutive > 10 {
			return fmt.Errorf("监控阈值无效：观察 10–1800 秒，最少请求 > 0，错误率 0–1，超时不超过 3600 秒")
		}
	}
	return nil
}
func operationSignature(nodes []DeliveryNode) string {
	values := map[string]any{}
	for _, n := range nodes {
		if n.Kind == "script" || n.Kind == "verify" || n.Monitor != nil {
			if n.Script != nil {
				copy := *n.Script
				copy.TargetDigest = ""
				copy.Target = ""
				n.Script = &copy
			}
			values[n.ID] = n
		}
	}
	return digestJSON(values)
}
func hasOperations(t *DeliveryTemplate) bool {
	return operationSignature(t.Nodes) != digestJSON(map[string]any{}) || operationSignature(t.Preparation) != digestJSON(map[string]any{})
}

func (s *Store) freezeOperations(p *DeliveryPlan, t *DeliveryTemplate) error {
	nodes := p.Nodes
	if p.ExecutionMode == dependencyExecution {
		nodes = append([]DeliveryNode(nil), nodes...)
		for i := range nodes {
			if orig := nodeByID(t.Nodes, nodes[i].ID); orig != nil {
				nodes[i].DependsOn = orig.DependsOn
			}
		}
	} // Dependencies are separately checked against the generated draft.
	if operationSignature(nodes) != operationSignature(t.Nodes) {
		return fmt.Errorf("验证、脚本、自动观察须使用管理员维护的模板配置；不能在交付草稿中替换")
	}
	for i := range p.Nodes {
		n := &p.Nodes[i]
		if _, skip := p.SkippedNodes[n.ID]; skip {
			continue
		}
		if err := validateOperationNode(*n); err != nil {
			return err
		}
		if n.Kind == "verify" {
			found := false
			for _, app := range p.Services {
				found = found || app.Service == n.Service
			}
			if !found {
				return fmt.Errorf("验证应用 %s 须在本次发布制品范围内", n.Service)
			}
		}
		if n.Kind == "script" {
			h := s.GetHost(n.Script.Host)
			if h == nil || h.Environment != p.Environment.Name || h.User == "" || strings.EqualFold(strings.TrimSpace(h.User), "root") || h.User == "0" || (h.KeyPath == "" && h.Password == "") {
				return fmt.Errorf("交付脚本须选择目标环境中已登记的非 root SSH 执行主机；不支持 SAE 宿主机")
			}
			copy := *n.Script
			copy.TargetDigest = s.scriptTargetDigest(h)
			if copy.TargetDigest == "" {
				return fmt.Errorf("脚本主机凭据不可读取")
			}
			copy.Target = h.User + "@" + h.Addr
			n.Script = &copy
			if p.Environment.Purpose == "production" && !p.Environment.Protected && !hasApprovalNodes(p) {
				return fmt.Errorf("生产脚本须配置环境保护或人工审批节点")
			}
		}
		if n.Monitor != nil {
			found := false
			for _, app := range p.Services {
				if app.Service == n.Service {
					found = true
					if !app.PendingTarget && !app.Target.PauseBetweenBatches {
						return fmt.Errorf("自动观察要求应用环境开启批次暂停")
					}
				}
			}
			if !found {
				return fmt.Errorf("自动观察应用不在本次发布范围")
			}
		}
	}
	return nil
}
func evidenceFor(run *DeliveryRun, n DeliveryNode, kind, actor, message string) *NodeEvidence {
	a := map[string]string{}
	for _, app := range run.Plan.Services {
		if n.Service == "" || app.Service == n.Service {
			a[app.Service] = app.Artifact.Digest
		}
	}
	return &NodeEvidence{Kind: kind, Actor: actor, At: time.Now().Unix(), PlanDigest: run.Digest, Artifacts: a, Message: message}
}
func (s *Store) privateOperationDigest(value any) string {
	raw, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Store) scriptTargetDigest(h *Host) string {
	if h == nil {
		return ""
	}
	var key []byte
	if h.KeyPath != "" {
		var err error
		key, err = os.ReadFile(h.KeyPath)
		if err != nil {
			return ""
		}
	}
	return s.privateOperationDigest([]any{h, key})
}
func indexDeliveryStep(run *DeliveryRun, id string) int {
	for i, s := range run.Steps {
		if s.NodeID == id {
			return i
		}
	}
	return -1
}
func (s *Store) preparationVerification(run *DeliveryRun, n DeliveryNode) (*DeliveryRun, error) {
	proof := *run
	proof.Plan.Services = nil
	for _, step := range run.Steps {
		if step.NodeID == n.ID {
			break
		}
		if step.Status != "success" || (step.Kind != "code-prepare" && step.Kind != "code-verify") {
			continue
		}
		p := s.CodePreparation(step.Detail)
		if p == nil || p.State != "ready" {
			continue
		}
		for _, build := range p.Builds {
			if build.Service != n.Service {
				continue
			}
			a := s.GetArtifact(build.ArtifactID)
			if a == nil || a.Service != n.Service || a.Digest == "" || a.Digest != build.ArtifactDigest {
				return nil, fmt.Errorf("验证制品已变化，请重新准备")
			}
			proof.Plan.Services = append(proof.Plan.Services, DeliveryService{Service: n.Service, Artifact: *a})
		}
	}
	if len(proof.Plan.Services) != 1 {
		return nil, fmt.Errorf("验证应用须有前置代码节点产生的唯一冻结制品；已有制品请在执行阶段验证")
	}
	return &proof, nil
}

// These checks never inherit proxy credentials or follow redirects. Loopback,
// metadata and link-local destinations are rejected at dial time (DNS rebinding
// cannot turn a validated hostname into an unrestricted request).
func operationHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() || ip.IP.IsMulticast() || ip.IP.String() == "100.100.100.200" || ip.IP.To4() != nil && ip.IP.To4()[0] == 0 {
				return nil, fmt.Errorf("禁止访问回环、链路本地及元数据地址")
			}
		}
		for _, ip := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("检查目标不可达")
	}}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
func readOperationHTTP(ctx context.Context, client *http.Client, raw string, query url.Values) (int, []byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return 0, nil, err
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("检查请求失败或超时；未放行")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10+1))
	if err != nil || len(body) > 64<<10 {
		return 0, nil, fmt.Errorf("检查响应不可用或超过 64 KiB")
	}
	return resp.StatusCode, body, nil
}
func verifyHTTP(ctx context.Context, client *http.Client, run *DeliveryRun, n DeliveryNode) (string, error) {
	v := n.Verification
	digest := ""
	for _, app := range run.Plan.Services {
		if app.Service == n.Service {
			digest = app.Artifact.Digest
		}
	}
	if digest == "" {
		return "", fmt.Errorf("验证没有冻结制品")
	}
	code, body, err := readOperationHTTP(ctx, client, v.URL, url.Values{"service": {n.Service}, "environment": {run.Plan.Environment.Name}, "artifact_digest": {digest}})
	if err != nil {
		return "", err
	}
	var result struct {
		Service        string `json:"service"`
		Environment    string `json:"environment"`
		ArtifactDigest string `json:"artifact_digest"`
		Passed         *bool  `json:"passed"`
	}
	if code != v.ExpectedStatus || json.Unmarshal(body, &result) != nil || result.Passed == nil || !*result.Passed || result.Service != n.Service || result.Environment != run.Plan.Environment.Name || result.ArtifactDigest != digest {
		return "", fmt.Errorf("HTTP 验证未通过，或响应未证明当前环境及制品；未放行")
	}
	return "HTTP 验证通过；响应身份与冻结制品一致", nil
}

type cappedScriptOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *cappedScriptOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(b.data) < 32<<10 {
		left := (32 << 10) - len(b.data)
		if len(p) > left {
			p = p[:left]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

var scriptEnvironmentRE = regexp.MustCompile(`(?i)(password|token|secret|authorization|access[_-]?key)\s*[:=]\s*[^\s]+`)

func (s *Store) redactScriptOutput(raw string) string {
	secrets := []string{}
	for _, c := range s.Connections() {
		if full := s.GetConnection(c.Name); full != nil {
			secrets = append(secrets, full.Password, full.PrivateKey, full.Passphrase)
		}
	}
	for _, h := range s.Hosts() {
		if full := s.GetHost(h.Name); full != nil {
			secrets = append(secrets, full.Password)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret != "" {
			raw = strings.ReplaceAll(raw, secret, "[REDACTED]")
		}
	}
	return scriptEnvironmentRE.ReplaceAllString(raw, "$1=[REDACTED]")
}
func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func deliverySSHClient(ctx context.Context, h Host) (*ssh.Client, error) {
	addr, err := normalizeSSHAddress(h.Addr)
	if err != nil {
		return nil, err
	}
	callback, err := secureHostKeyCallback(h)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{User: h.User, HostKeyCallback: callback}
	if h.KeyPath != "" {
		key, e := os.ReadFile(h.KeyPath)
		if e != nil {
			return nil, e
		}
		signer, e := ssh.ParsePrivateKey(key)
		if e != nil {
			return nil, e
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	} else if h.Password != "" {
		cfg.Auth = []ssh.AuthMethod{ssh.Password(h.Password)}
	} else {
		return nil, fmt.Errorf("脚本执行主机须显式配置凭据")
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, channels, requests, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(client, channels, requests), nil
}
func executeDeliverySSH(ctx context.Context, h Host, script DeliveryScript, run *DeliveryRun) (string, error) {
	client, err := deliverySSHClient(ctx, h)
	if err != nil {
		return "", fmt.Errorf("SSH 执行主机连接失败")
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH 会话创建失败")
	}
	defer session.Close()
	// Script text goes through stdin, arguments are individually shell quoted.
	// Do not inject the platform's process environment or cloud credentials.
	session.Stdin = strings.NewReader(script.Content)
	var output cappedScriptOutput
	session.Stdout = &output
	session.Stderr = &output
	command := "env -i PATH=/usr/local/bin:/usr/bin:/bin DELIVERY_ENVIRONMENT=" + shellArgument(run.Plan.Environment.Name) + " DELIVERY_RUN_ID=" + shellArgument(strconv.FormatInt(run.ID, 10)) + " sh -s --"
	for _, a := range script.Arguments {
		command += " " + shellArgument(a)
	}
	if err = session.Start(command); err != nil {
		return "", fmt.Errorf("SSH 脚本启动结果不确定，必须核对，禁止重放")
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case err = <-done:
		if err != nil {
			return string(output.data), fmt.Errorf("脚本非零退出或连接中断；不能自动重试")
		}
		return string(output.data), nil
	case <-ctx.Done():
		_ = session.Close()
		_ = client.Close()
		<-done
		return string(output.data), fmt.Errorf("脚本超时；远程进程可能仍运行，必须核对，禁止重放")
	}
}

func (s *Store) advanceOperation(run *DeliveryRun, step *DeliveryStep, n DeliveryNode, actor string) error {
	if step.Status == "executing" || step.Status == "uncertain" {
		return fmt.Errorf("节点执行中或中断待核对，禁止重复执行")
	}
	if n.Kind == "verify" && n.Verification.Mode == "manual" {
		return fmt.Errorf("请由指定验证人提交独立验证结论")
	}
	if n.Kind == "script" {
		h := s.GetHost(n.Script.Host)
		if h == nil || s.scriptTargetDigest(h) == "" || s.scriptTargetDigest(h) != n.Script.TargetDigest {
			return fmt.Errorf("脚本主机或凭据已变化，请重新提交")
		}
	}
	step.Status, run.Status = "executing", "running"
	if n.Kind == "script" {
		step.LeaseUntil = time.Now().Unix() + int64(n.Script.TimeoutSeconds) + 15
	}
	var claimErr error
	if n.Kind == "script" {
		claimErr = s.claimScriptOperation(run, n)
	} else {
		claimErr = s.saveDeliveryRun(run)
	}
	if claimErr != nil {
		return claimErr
	}
	if n.Kind == "script" {
		worker := *run
		worker.Steps = append([]DeliveryStep{}, run.Steps...)
		index := indexDeliveryStep(&worker, n.ID)
		go func() { _ = s.performDeliveryOperation(&worker, &worker.Steps[index], n, actor) }()
		return nil
	}
	return s.performDeliveryOperation(run, step, n, actor)
}

// Serialize scripts per host across processes. Uncertain results keep the
// reservation until an operator reconciles them; expiry never replays writes.
func (s *Store) claimScriptOperation(run *DeliveryRun, n DeliveryNode) error {
	// Recover only a proven completed reservation (e.g. process exit between
	// persisting success and releasing the host). Never reap an uncertain write.
	var previous int64
	var previousNode string
	if s.db.QueryRow(`SELECT run_id,node_id FROM delivery_script_locks WHERE host=?`, n.Script.Host).Scan(&previous, &previousNode) == nil {
		if owner := s.DeliveryRun(previous); owner != nil {
			for _, step := range owner.Steps {
				if step.NodeID == previousNode && step.Evidence != nil && (step.Status == "success" || step.Evidence.Kind == "manual-script-reconciliation") {
					s.db.Exec(`DELETE FROM delivery_script_locks WHERE host=? AND run_id=? AND node_id=?`, n.Script.Host, previous, previousNode)
				}
			}
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO delivery_script_locks(host,run_id,node_id) VALUES(?,?,?)`, n.Script.Host, run.ID, n.ID); err != nil {
		return fmt.Errorf("执行主机已有脚本或待核对结果，请先处理原节点；不会并发执行")
	}
	raw, _ := json.Marshal(run.Steps)
	r, err := tx.Exec(`UPDATE delivery_runs SET status=?,execution=?,revision=revision+1 WHERE id=? AND revision=?`, run.Status, string(raw), run.ID, run.Revision)
	if err != nil {
		return err
	}
	count, _ := r.RowsAffected()
	if count != 1 {
		return ErrRunConflict
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	run.Revision++
	return nil
}
func (s *Store) releaseScriptOperation(run *DeliveryRun, n DeliveryNode) {
	s.db.Exec(`DELETE FROM delivery_script_locks WHERE host=? AND run_id=? AND node_id=?`, n.Script.Host, run.ID, n.ID)
}
func (s *Store) performDeliveryOperation(run *DeliveryRun, step *DeliveryStep, n DeliveryNode, actor string) error {
	var detail string
	var err error
	if n.Kind == "script" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(n.Script.TimeoutSeconds)*time.Second)
		h := s.GetHost(n.Script.Host)
		if h == nil || s.scriptTargetDigest(h) == "" || s.scriptTargetDigest(h) != n.Script.TargetDigest {
			err = fmt.Errorf("脚本目标已变化，未执行")
		} else {
			detail, err = executeDeliverySSH(ctx, *h, *n.Script, run)
		}
		cancel()
		detail = s.redactScriptOutput(detail)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(n.Verification.TimeoutSeconds)*time.Second)
		detail, err = verifyHTTP(ctx, operationHTTPClient(time.Duration(n.Verification.TimeoutSeconds)*time.Second), run, n)
		cancel()
	}
	step.Status = "success"
	step.LeaseUntil = 0
	step.Detail = detail
	step.EndedAt = time.Now().Unix()
	if err != nil {
		step.Status, run.Status = "failed", deliveryFailureStatus(run)
		if n.Kind == "script" {
			step.Status, run.Status = "uncertain", "failed"
		}
		step.Detail += "\n" + err.Error()
	} else {
		step.Evidence = evidenceFor(run, n, map[bool]string{true: "script-exit", false: "http-verification"}[n.Kind == "script"], actor, detail)
	}
	if e := s.saveDeliveryRun(run); e != nil {
		return e
	}
	if n.Kind == "script" && err == nil {
		s.releaseScriptOperation(run, n)
	}
	s.Audit(actor, "execute-"+n.Kind, "delivery-run", fmt.Sprint(run.ID), map[string]any{"node": n.ID, "status": step.Status, "digest": run.Digest})
	return nil
}

func deliveryOperationStep(run *DeliveryRun, id string) (*DeliveryStep, *DeliveryNode) {
	if run.Plan.ExecutionMode != dependencyExecution {
		return researchNextNode(run)
	}
	for i := range run.Steps {
		if run.Steps[i].NodeID == id {
			return &run.Steps[i], nodeByID(run.Plan.Nodes, id)
		}
	}
	return nil, nil
}
func (s *Store) reviewVerification(id int64, nodeID, digest, actor, comment string, passed bool) (*DeliveryRun, error) {
	run := s.DeliveryRun(id)
	if run == nil || run.Digest != digest || (run.Status != "approved" && run.Status != "running") {
		return nil, fmt.Errorf("交付状态已变化")
	}
	if err := s.checkDeliveryPolicy(&run.Plan); err != nil {
		return nil, err
	}
	if run.Plan.ExecutionMode == dependencyExecution {
		for _, other := range run.Steps {
			if other.NodeID != nodeID && blocksDisposition(other) {
				return nil, fmt.Errorf("请先核对在途执行回执")
			}
		}
	}
	step, n := deliveryOperationStep(run, nodeID)
	if n == nil || n.Verification == nil || (run.Plan.ExecutionMode == dependencyExecution && (blockedPredecessor(run, n) != "" || step.Status != "pending" && step.Status != "reviewing")) || n.ID != nodeID || n.Kind != "verify" || n.Verification.Mode != "manual" || !namedApprover(n, actor) || strings.TrimSpace(comment) == "" || len(comment) > 8000 {
		return nil, fmt.Errorf("仅当前节点指定验证人可提交，须填写验证记录或证据链接")
	}
	for _, r := range step.Reviews {
		if r.Actor == actor {
			return nil, fmt.Errorf("已经提交验证结论")
		}
	}
	step.Reviews = append(step.Reviews, ApprovalDecision{Actor: actor, Approved: passed, Comment: comment, At: time.Now().Unix()})
	if !passed {
		step.Status, run.Status = "failed", deliveryFailureStatus(run)
	} else {
		approved := map[string]bool{}
		for _, r := range step.Reviews {
			if r.Approved {
				approved[r.Actor] = true
			}
		}
		if n.ApprovalMode == "any" || len(approved) == len(n.Approvers) {
			step.Status = "success"
			step.EndedAt = time.Now().Unix()
			step.Evidence = evidenceFor(run, *n, "manual-verification", actor, "人工验证，不代表机器测试证明")
		} else {
			step.Status = "reviewing"
		}
	}
	if step.StartedAt == 0 {
		step.StartedAt = time.Now().Unix()
	}
	if err := s.saveDeliveryRun(run); err != nil {
		return nil, err
	}
	s.Audit(actor, "verify-delivery", "delivery-run", fmt.Sprint(id), map[string]any{"node": nodeID, "passed": passed, "digest": digest})
	return run, nil
}
func registerOperationRoutes(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("POST /api/delivery/runs/{id}/reconcile-script", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var req struct {
			NodeID  string `json:"node_id"`
			Digest  string `json:"digest"`
			Outcome string `json:"outcome"`
			Comment string `json:"comment"`
			Confirm bool   `json:"confirm"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil {
			deliveryWriteError(w, 400, "请求无效")
			return
		}
		run := s.DeliveryRun(id)
		if run == nil || !deliveryManage(r, run.Owner) || !req.Confirm || run.Digest != req.Digest || strings.TrimSpace(req.Comment) == "" || len(req.Comment) > 8000 || (req.Outcome != "success" && req.Outcome != "failed") {
			deliveryWriteError(w, 403, "请由发起人或管理员核对快照，填写核对依据并明确确认结果")
			return
		}
		control := s.DeliveryControl(id)
		if control.State == "running" || control.State == "pausing" {
			deliveryWriteError(w, 409, "请先暂停自动推进")
			return
		}
		step, n := deliveryOperationStep(run, req.NodeID)
		if n == nil || n.ID != req.NodeID || n.Kind != "script" || (step.Status != "uncertain" && !(step.Status == "executing" && step.LeaseUntil < time.Now().Unix())) {
			deliveryWriteError(w, 409, "当前节点不是可核对的脚本，或执行租约尚未结束")
			return
		}
		step.Status = req.Outcome
		step.EndedAt = time.Now().Unix()
		step.LeaseUntil = 0
		step.Detail += "\n人工核对：" + req.Comment
		step.Reviews = append(step.Reviews, ApprovalDecision{Actor: deliveryActor(r), Approved: req.Outcome == "success", Comment: req.Comment, At: step.EndedAt})
		step.Evidence = evidenceFor(run, *n, "manual-script-reconciliation", deliveryActor(r), "人工核对，不是自动执行证明；没有重放脚本")
		if req.Outcome == "success" {
			run.Status = "running"
		} else {
			run.Status = "failed"
		}
		if err := s.saveDeliveryRun(run); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		s.releaseScriptOperation(run, *n)
		s.Audit(deliveryActor(r), "reconcile-delivery-script", "delivery-run", fmt.Sprint(id), map[string]any{"node": n.ID, "outcome": req.Outcome, "digest": run.Digest})
		deliveryJSON(w, run)
	})
	mux.HandleFunc("GET /api/delivery/runs/{id}/observations", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		rows, err := s.db.Query(`SELECT document,revision FROM delivery_observations WHERE run_id=? ORDER BY observation_key LIMIT 100`, id)
		if err != nil {
			deliveryWriteError(w, 500, "观察记录读取失败")
			return
		}
		defer rows.Close()
		out := []BatchObservation{}
		for rows.Next() {
			var raw string
			var rev int64
			if rows.Scan(&raw, &rev) != nil {
				deliveryWriteError(w, 500, "观察记录读取失败")
				return
			}
			var v BatchObservation
			if json.Unmarshal([]byte(raw), &v) != nil {
				deliveryWriteError(w, 500, "观察记录损坏")
				return
			}
			v.Revision = rev
			out = append(out, v)
		}
		if rows.Err() != nil {
			deliveryWriteError(w, 500, "观察记录读取失败")
			return
		}
		deliveryJSON(w, out)
	})
	mux.HandleFunc("POST /api/delivery/runs/{id}/verify", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var v struct {
			NodeID  string `json:"node_id"`
			Digest  string `json:"digest"`
			Comment string `json:"comment"`
			Passed  bool   `json:"passed"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&v) != nil {
			deliveryWriteError(w, 400, "请求无效")
			return
		}
		run := s.DeliveryRun(id)
		if run == nil {
			deliveryWriteError(w, 404, "执行单不存在")
			return
		}
		if err := validateDeliveryReviewerDirectory(&run.Plan, r.Header.Get("X-Reviewer-Directory"), run.Owner); err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		out, err := s.reviewVerification(id, v.NodeID, v.Digest, deliveryActor(r), v.Comment, v.Passed)
		if err != nil {
			deliveryWriteError(w, 409, err)
			return
		}
		deliveryJSON(w, out)
	})
}
