package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var managedArthasIdentity = regexp.MustCompile(`^[a-f0-9]{40}$`)

func managedArthasEndpoint(endpoint string) (id, fingerprint string, ok bool) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "deployment" || !managedArthasIdentity.MatchString(u.Host) || !managedArthasIdentity.MatchString(strings.TrimPrefix(u.Path, "/")) || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false
	}
	return u.Host, strings.TrimPrefix(u.Path, "/"), true
}
func (m *arthasConsole) deploymentRequest(ctx context.Context, actor, method, path string, body io.Reader, timeout time.Duration) (*http.Response, error) {
	if m.st.arthasDeployURL == "" {
		return nil, errors.New("尚未配置内部部署服务，无法读取部署实例")
	}
	req, e := http.NewRequestWithContext(ctx, method, m.st.arthasDeployURL+"/api/arthas-managed/instances"+path, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("X-User", actor)
	req.Header.Set("X-Role", "admin")
	req.Header.Set("Content-Type", "application/json")
	client := *m.client
	client.Timeout = timeout
	// The ordinary diagnostic transport waits only 13 seconds for headers.
	// Inspection/installation have their own bounded budgets; changing only
	// Client.Timeout would still abandon a slow attach at that shorter limit.
	if transport, ok := m.client.Transport.(*http.Transport); ok {
		owned := transport.Clone()
		owned.ResponseHeaderTimeout = timeout
		owned.DisableKeepAlives = true
		client.Transport = owned
	}
	return client.Do(req)
}
func (m *arthasConsole) deployedInstances(w http.ResponseWriter, r *http.Request) {
	path := "?" + r.URL.Query().Encode()
	if id := r.PathValue("id"); id != "" {
		if !managedArthasIdentity.MatchString(id) {
			arthasError(w, 400, "实例标识无效")
			return
		}
		path = "/" + id
	}
	res, e := m.deploymentRequest(r.Context(), r.Header.Get("X-User"), "GET", path, nil, 30*time.Second)
	if e != nil {
		arthasError(w, 503, "部署实例服务不可用，请稍后刷新")
		return
	}
	defer res.Body.Close()
	copyArthasDeploymentReply(w, res)
}
func copyArthasDeploymentReply(w http.ResponseWriter, res *http.Response) {
	body, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(body) > 1<<20 || !json.Valid(body) {
		arthasError(w, 502, "部署实例响应无效")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	w.Write(body)
}
func (m *arthasConsole) attachDeployed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Fingerprint string `json:"fingerprint"`
	}
	if !managedArthasIdentity.MatchString(id) {
		arthasError(w, 400, "实例标识无效")
		return
	}
	if !arthasDecode(w, r, &in) {
		return
	}
	if !managedArthasIdentity.MatchString(in.Fingerprint) {
		arthasError(w, 400, "请重新检测并选择 JVM")
		return
	}
	if m.audit(r.Header.Get("X-User"), id, "attach", "", "starting") != nil {
		arthasError(w, 500, "无法记录诊断审计，未接入实例")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 165*time.Second)
	defer cancel()
	body, _ := json.Marshal(in)
	res, e := m.deploymentRequest(ctx, r.Header.Get("X-User"), "POST", "/"+id+"/attach", strings.NewReader(string(body)), 165*time.Second)
	if e != nil {
		m.audit(r.Header.Get("X-User"), id, "attach", "", "unknown")
		arthasError(w, 502, "接入响应未确认；不会自动重试。请重新检测并核对接入状态")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		m.audit(r.Header.Get("X-User"), id, "attach", "", "failed")
		copyArthasDeploymentReply(w, res)
		return
	}
	var deployed struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Service     string `json:"service"`
		Environment string `json:"environment"`
		Fingerprint string `json:"fingerprint"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&deployed) != nil || deployed.ID != id || deployed.Fingerprint != in.Fingerprint || deployed.Name == "" || deployed.Service == "" || deployed.Environment == "" {
		arthasError(w, 502, "接入结果身份不一致，未登记诊断连接")
		return
	}
	hash := sha256.Sum256([]byte(id + "/" + in.Fingerprint))
	targetID := hex.EncodeToString(hash[:24])
	t := arthasTarget{ID: targetID, Name: deployed.Name, Service: deployed.Service, Environment: deployed.Environment, Endpoint: "deployment://" + id + "/" + in.Fingerprint, Revision: 1, UpdatedAt: time.Now().Unix(), Managed: true}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, err := m.st.arthasTarget(targetID)
	if err == nil {
		writeJSON(w, previous)
		return
	}
	tx, e := m.st.db.Begin()
	if e != nil {
		arthasError(w, 500, "实例已接入，但连接登记失败，请核对")
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO arthas_targets(id,name,service,environment,endpoint,revision,updated_at,deleted) VALUES(?,?,?,?,?,?,?,0) ON CONFLICT(id) DO UPDATE SET deleted=0,revision=arthas_targets.revision+1,updated_at=excluded.updated_at`, t.ID, t.Name, t.Service, t.Environment, t.Endpoint, t.Revision, t.UpdatedAt)
	if e == nil {
		_, e = tx.Exec(`INSERT INTO arthas_audit(id,actor,target_id,action,command_name,command_hash,state,at) VALUES(?,?,?,?,?,?,?,?)`, arthasID(), r.Header.Get("X-User"), targetID, "attach", "", "", "success", t.UpdatedAt)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		arthasError(w, 500, "实例已接入，但连接登记失败，请核对")
		return
	}
	saved, e := m.st.arthasTarget(targetID)
	if e != nil {
		arthasError(w, 500, "连接读取失败")
		return
	}
	writeJSON(w, saved)
}
