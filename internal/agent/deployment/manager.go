package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"logplatform/pkg/deploytask"
)

type Manager struct {
	enrollmentDone bool
	cfg            Config
	client         *http.Client
	lease          *os.File
	launch         func(string, Config) error
}

func Open(c Config) (*Manager, error) {
	u, e := url.Parse(c.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid deployment broker URL")
	}
	if c.Token == "" || c.AgentID == "" || c.Host == "" || c.Environment == "" || len(c.AllowedRoots) == 0 || !filepath.IsAbs(c.StateDir) {
		return nil, fmt.Errorf("deployment requires independent credential, identity, scope and local roots")
	}
	if c.WorkerMode == "" {
		c.WorkerMode = "systemd"
	}
	if c.MaxParallel == 0 {
		c.MaxParallel = 1
	}
	if c.MaxParallel < 1 || c.MaxParallel > 8 {
		return nil, fmt.Errorf("invalid deployment concurrency")
	}
	if c.WorkerMode != "systemd" && c.WorkerMode != "isolated-process" {
		return nil, fmt.Errorf("invalid deployment worker mode")
	}
	if c.WorkerMode == "isolated-process" && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return nil, fmt.Errorf("isolated process workers require a loopback broker")
	}
	for _, root := range c.AllowedRoots {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
			return nil, fmt.Errorf("invalid local deployment policy")
		}
	}
	if e = os.MkdirAll(c.StateDir, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(c.StateDir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid deployment state directory")
	}
	if e = os.Chmod(c.StateDir, 0700); e != nil {
		return nil, e
	}
	f, e := lock(filepath.Join(c.StateDir, ".manager.lock"))
	if e != nil {
		return nil, fmt.Errorf("deployment module already owns this state directory: %w", e)
	}
	var owner struct {
		UID int `json:"uid"`
	}
	if e = readJSON(filepath.Join(c.StateDir, "owner.json"), &owner); e == nil {
		if owner.UID != currentUID() {
			f.Close()
			return nil, fmt.Errorf("deployment execution user changed; explicit migration is required")
		}
	} else if os.IsNotExist(e) {
		owner.UID = currentUID()
		if e = atomicJSON(filepath.Join(c.StateDir, "owner.json"), owner); e != nil {
			f.Close()
			return nil, e
		}
	} else {
		f.Close()
		return nil, e
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &Manager{cfg: c, client: client, lease: f, launch: startWorker}, nil
}
func (m *Manager) Close() error { return m.lease.Close() }
func (m *Manager) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(m.cfg.URL, "/")+path, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+m.cfg.Token)
	r.Header.Set("X-Deployment-Agent-ID", m.cfg.AgentID)
	r.Header.Set("Content-Type", "application/json")
	if strings.HasSuffix(path, "/artifact") {
		client := *m.client
		client.Timeout = 15 * time.Minute
		return client.Do(r)
	}
	return m.client.Do(r)
}
func verifyArtifact(path string, t deploytask.Task) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, t.ArtifactSize+1))
	if e != nil {
		return e
	}
	if n != t.ArtifactSize || "sha256:"+hex.EncodeToString(h.Sum(nil)) != t.ArtifactDigest {
		return fmt.Errorf("artifact identity mismatch")
	}
	return nil
}
func (m *Manager) accept(t deploytask.Task) error {
	if e := t.Validate(); e != nil {
		return e
	}
	if t.AgentID != m.cfg.AgentID || t.Host != m.cfg.Host || t.Environment != m.cfg.Environment {
		return fmt.Errorf("deployment task is outside this Agent scope")
	}
	if e := validateRoot(t.Root, m.cfg.AllowedRoots); e != nil {
		return e
	}
	dir := filepath.Join(m.cfg.StateDir, t.ID)
	var old job
	if e := readJSON(filepath.Join(dir, "job.json"), &old); e == nil {
		if old.Digest != deploytask.Digest(t) {
			return fmt.Errorf("task ID was reused with another plan")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	entries, e := os.ReadDir(m.cfg.StateDir)
	if e != nil {
		return e
	}
	if len(entries) > 1000 {
		return fmt.Errorf("deployment journal capacity reached; existing tasks retained")
	}
	if e = os.Mkdir(dir, 0700); e != nil {
		return e
	}
	if e = os.Mkdir(filepath.Join(dir, "outbox"), 0700); e != nil {
		return e
	}
	policy := m.cfg
	policy.Token = ""
	j := job{Task: t, Digest: deploytask.Digest(t), Policy: policy}
	if e = atomicJSON(filepath.Join(dir, "job.json"), j); e != nil {
		return e
	}
	return appendReport(dir, j, "accepted", "accepted", "Frozen deployment task persisted")
}
func (m *Manager) prepare(ctx context.Context, dir string, j job) error {
	if j.Task.Validate() != nil || j.Digest != deploytask.Digest(j.Task) || j.Task.AgentID != m.cfg.AgentID || j.Task.Host != m.cfg.Host || j.Task.Environment != m.cfg.Environment {
		return fmt.Errorf("stored task identity or digest changed; execution refused")
	}
	if e := validateRoot(j.Task.Root, m.cfg.AllowedRoots); e != nil {
		return e
	}
	if _, e := os.Lstat(filepath.Join(dir, "intent")); e == nil {
		return reconcile(dir, j)
	} else if !os.IsNotExist(e) {
		return e
	}
	entries, e := os.ReadDir(m.cfg.StateDir)
	if e != nil {
		return e
	}
	active := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		other := filepath.Join(m.cfg.StateDir, entry.Name())
		if _, e := os.Stat(filepath.Join(other, "intent")); e != nil {
			continue
		}
		r, e := latest(other)
		if e != nil {
			return e
		}
		if !deploytask.Terminal(r.State) {
			active++
		}
	}
	if active >= m.cfg.MaxParallel {
		return nil
	}
	if e := verifyArtifact(filepath.Join(dir, "artifact"), j.Task); e != nil {
		free, e := diskAvailable(dir)
		if e != nil {
			return e
		}
		required := j.Task.ArtifactSize + (64 << 20)
		if j.Task.Bundle != nil {
			required += 2 * j.Task.Bundle.ExpandedSize
		}
		if free < uint64(required) {
			return fmt.Errorf("insufficient deployment staging space; task retained without execution")
		}
		resp, e := m.request(ctx, http.MethodGet, "/tasks/"+j.Task.ID+"/artifact", nil)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("artifact fetch returned %d", resp.StatusCode)
		}
		file, e := os.OpenFile(filepath.Join(dir, "artifact.part"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(file, io.LimitReader(resp.Body, j.Task.ArtifactSize+1))
		if e == nil {
			e = file.Sync()
		}
		closeErr := file.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
		if e = verifyArtifact(filepath.Join(dir, "artifact.part"), j.Task); e != nil {
			return e
		}
		if e = os.Rename(filepath.Join(dir, "artifact.part"), filepath.Join(dir, "artifact")); e != nil {
			return e
		}
		if e = syncDir(dir); e != nil {
			return e
		}
	}
	for name, body := range j.Task.Files {
		p := filepath.Join(dir, "files", name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString(body)
		if e == nil {
			e = f.Sync()
		}
		f.Close()
		if e != nil {
			return e
		}
	}
	// Once durable intent exists, a crash in the fork/receipt gap is unknown.
	// Conservatively reconcile it; never start the same task a second time.
	f, e := os.OpenFile(filepath.Join(dir, "intent"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.WriteString(j.Digest)
	if e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return e
	}
	if e = syncDir(dir); e != nil {
		return e
	}
	if e = m.launch(dir, m.cfg); e != nil {
		return appendReport(dir, j, "needs_review", "dispatch", scrub(j, "Independent worker launch was not confirmed; no automatic retry: "+e.Error()))
	}
	return nil
}
func (m *Manager) flush(ctx context.Context, dir string) error {
	var ack deploytask.Ack
	e := readJSON(filepath.Join(dir, "ack.json"), &ack)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	all, e := reports(dir)
	if e != nil {
		return e
	}
	for _, r := range all {
		if r.Sequence <= ack.Sequence {
			continue
		}
		body, _ := json.Marshal(r)
		resp, e := m.request(ctx, http.MethodPost, "/reports", body)
		if e != nil {
			return e
		}
		var next deploytask.Ack
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&next)
		resp.Body.Close()
		if resp.StatusCode != 200 || decodeErr != nil || next.TaskID != r.TaskID || next.Sequence != r.Sequence {
			return fmt.Errorf("deployment report ACK was not confirmed")
		}
		if e = atomicJSON(filepath.Join(dir, "ack.json"), next); e != nil {
			return e
		}
		ack = next
	}
	return nil
}

// Tick first reconciles local receipts and drains the durable outbox. A failed
// broker request never removes a task, restarts an application or loses an ACK.
func (m *Manager) Tick(ctx context.Context) error {
	entries, e := os.ReadDir(m.cfg.StateDir)
	if e != nil {
		return e
	}
	var first error
	for _, entry := range entries {
		if !entry.IsDir() || !deploytask.IDPattern.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(m.cfg.StateDir, entry.Name())
		var j job
		if e = readJSON(filepath.Join(dir, "job.json"), &j); e != nil {
			return e
		}
		if e = m.prepare(ctx, dir, j); e != nil && first == nil {
			first = e
		}
		if e = m.flush(ctx, dir); e != nil && first == nil {
			first = e
		}
	}
	if e := m.enroll(ctx); e != nil {
		return e
	}
	resp, e := m.request(ctx, http.MethodGet, "/tasks", nil)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("deployment poll returned %d", resp.StatusCode)
	}
	var tasks []deploytask.Task
	if e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&tasks); e != nil {
		return e
	}
	if len(tasks) > 32 {
		return fmt.Errorf("oversized deployment batch")
	}
	for _, t := range tasks {
		if e = m.accept(t); e != nil {
			return e
		}
	}
	return first
}
func (m *Manager) Run(ctx context.Context, logError func(error)) {
	for {
		if e := m.Tick(ctx); e != nil && ctx.Err() == nil && logError != nil {
			logError(e)
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
