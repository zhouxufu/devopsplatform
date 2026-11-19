package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"logplatform/pkg/deploytask"
	"os"
	"path/filepath"
	"time"
)

func (c Config) EnrollmentScope() deploytask.EnrollmentScope {
	return deploytask.EnrollmentScope{OperationID: c.EnrollmentID, AgentID: c.AgentID, Host: c.Host, Environment: c.Environment, Roots: c.AllowedRoots, StateDir: c.StateDir, Launcher: c.WorkerLauncher}
}

type workerCheck struct {
	Policy Config `json:"policy"`
	Nonce  string `json:"nonce"`
	UID    int    `json:"uid"`
}
type workerCheckReceipt struct {
	Nonce       string `json:"nonce"`
	UID         int    `json:"uid"`
	Independent bool   `json:"independent"`
}

// RunWorkerCheck is a fixed, data-only worker: it never reads a deployment job
// or runs an application command. A separate unit and unchanged UID are required.
func RunWorkerCheck(dir string) error {
	var check workerCheck
	if e := readJSON(filepath.Join(dir, "check.json"), &check); e != nil {
		return e
	}
	if check.Nonce != filepath.Base(dir) || check.UID != currentUID() || check.Policy.WorkerMode != "systemd" || filepath.Dir(dir) != filepath.Join(check.Policy.StateDir, ".checks") || !isolatedWorkerGroup(dir, check.Policy) {
		return fmt.Errorf("worker check identity or isolation mismatch")
	}
	return atomicJSON(filepath.Join(dir, "receipt.json"), workerCheckReceipt{check.Nonce, currentUID(), true})
}

// CheckEnrollmentWorker proves the actual production launcher without any
// application side effects. Only its own freshly created probe directory is removed.
func CheckEnrollmentWorker(c Config) error {
	if c.WorkerMode != "systemd" || c.WorkerLauncher == "" {
		return fmt.Errorf("deployment enrollment requires the fixed systemd launcher")
	}
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return e
	}
	nonce := hex.EncodeToString(random[:])
	parent := filepath.Join(c.StateDir, ".checks")
	if e := os.MkdirAll(parent, 0700); e != nil {
		return e
	}
	if p, e := filepath.EvalSymlinks(parent); e != nil || p != parent {
		return fmt.Errorf("worker check path traverses a link")
	}
	dir := filepath.Join(parent, nonce)
	if e := os.Mkdir(dir, 0700); e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	if e := atomicJSON(filepath.Join(dir, "check.json"), workerCheck{c, nonce, currentUID()}); e != nil {
		return e
	}
	if e := launchEnrolledWorker(dir, c, true); e != nil {
		return e
	}
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		var proof workerCheckReceipt
		if readJSON(filepath.Join(dir, "receipt.json"), &proof) == nil {
			if proof.Nonce != nonce || proof.UID != currentUID() || !proof.Independent {
				return fmt.Errorf("worker check receipt mismatch")
			}
			return atomicJSON(filepath.Join(c.StateDir, "enrollment-proof.json"), deploytask.EnrollmentProof{EnrollmentScope: c.EnrollmentScope(), WorkerVerified: true})
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("independent worker check was not confirmed")
}

func (m *Manager) enroll(ctx context.Context) error {
	if m.cfg.EnrollmentID == "" || m.enrollmentDone {
		return nil
	}
	var proof deploytask.EnrollmentProof
	if readJSON(filepath.Join(m.cfg.StateDir, "enrollment-proof.json"), &proof) != nil || !proof.WorkerVerified || !sameScope(proof.EnrollmentScope, m.cfg.EnrollmentScope()) {
		return fmt.Errorf("deployment enrollment has no matching independent-worker proof")
	}
	body, _ := json.Marshal(proof)
	r, e := m.request(ctx, "POST", "/enrollment", body)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("deployment enrollment callback returned %d", r.StatusCode)
	}
	m.enrollmentDone = true
	return nil
}
func sameScope(a, b deploytask.EnrollmentScope) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func EnrollmentIdle(state string) error {
	entries, e := os.ReadDir(state)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !entry.IsDir() || !deploytask.IDPattern.MatchString(entry.Name()) {
			continue
		}
		r, e := latest(filepath.Join(state, entry.Name()))
		if e != nil || !deploytask.Terminal(r.State) {
			return fmt.Errorf("deployment tasks are active or uncertain; enrollment changes refused")
		}
	}
	return nil
}

// PrepareEnrollmentState validates the fixed policy and UID without acquiring
// the running manager's lease. Rotating credentials must not require stopping
// an otherwise healthy collector merely to prepare its replacement config.
func PrepareEnrollmentState(c Config) error {
	if c.WorkerMode != "systemd" || c.WorkerLauncher != "/usr/local/libexec/logagent-deployment-worker" || !filepath.IsAbs(c.StateDir) || filepath.Clean(c.StateDir) != c.StateDir || c.StateDir == "/" || len(c.AllowedRoots) == 0 {
		return fmt.Errorf("invalid enrollment worker policy")
	}
	for _, root := range c.AllowedRoots {
		if e := validateRoot(root, c.AllowedRoots); e != nil {
			return e
		}
	}
	if e := os.MkdirAll(c.StateDir, 0700); e != nil {
		return e
	}
	if resolved, e := filepath.EvalSymlinks(c.StateDir); e != nil || resolved != c.StateDir {
		return fmt.Errorf("deployment state traverses a link")
	}
	var owner struct {
		UID int `json:"uid"`
	}
	e := readJSON(filepath.Join(c.StateDir, "owner.json"), &owner)
	if e == nil {
		if owner.UID != currentUID() {
			return fmt.Errorf("deployment execution user cannot change")
		}
		return nil
	}
	if !os.IsNotExist(e) {
		return e
	}
	entries, e := os.ReadDir(c.StateDir)
	if e != nil {
		return e
	}
	if len(entries) > 0 {
		return fmt.Errorf("existing deployment state has no verified owner")
	}
	owner.UID = currentUID()
	return atomicJSON(filepath.Join(c.StateDir, "owner.json"), owner)
}
