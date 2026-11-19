// Package deploytask defines the frozen, host-scoped deployment Agent protocol.
package deploytask

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const Version = 2

type Step struct {
	Name   string `json:"name"`
	Script string `json:"script"`
	// A nonzero exit can mean an incomplete stop/start, not a known failure.
	UncertainOnError bool `json:"uncertain_on_error,omitempty"`
	RollbackOnExit   bool `json:"rollback_on_exit,omitempty"`
	SyncRelease      bool `json:"sync_release,omitempty"`
}

// Tasks are created only from reviewed release snapshots. The Agent credential
// can fetch/report its own tasks, but cannot submit scripts or create releases.
type RollbackPlan struct {
	Guard string `json:"guard"`
	Steps []Step `json:"steps"`
	Probe string `json:"probe"`
}

type Task struct {
	ReleaseID      int64             `json:"release_id,omitempty"`
	Bundle         *BundleDescriptor `json:"bundle,omitempty"`
	Rollback       *RollbackPlan     `json:"rollback,omitempty"`
	Version        int               `json:"version"`
	ID             string            `json:"id"`
	DeployID       int64             `json:"deploy_id"`
	AgentID        string            `json:"agent_id"`
	Host           string            `json:"host"`
	Service        string            `json:"service"`
	Environment    string            `json:"environment"`
	Root           string            `json:"root"`
	ArtifactDigest string            `json:"artifact_digest"`
	ArtifactSize   int64             `json:"artifact_size"`
	Files          map[string]string `json:"files,omitempty"`
	Secrets        []string          `json:"secrets,omitempty"`
	Steps          []Step            `json:"steps"`
	// Exit 0 = machine verified; 10 = starting; 20 = known dead; other = unknown.
	Probe            string `json:"probe"`
	WarnAfterSeconds int    `json:"warn_after_seconds"`
}

type Report struct {
	TaskID     string `json:"task_id"`
	TaskDigest string `json:"task_digest"`
	Sequence   int64  `json:"sequence"`
	State      string `json:"state"`
	Phase      string `json:"phase"`
	Message    string `json:"message,omitempty"`
	At         int64  `json:"at"`
}

type Ack struct {
	TaskID   string `json:"task_id"`
	Sequence int64  `json:"sequence"`
}

var IDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func Digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func Terminal(s string) bool {
	return s == "success" || s == "failed" || s == "rolled-back" || s == "rollback-failed"
}

func (t Task) Validate() error {
	if (t.Version != Version && t.Version != 1) || !IDPattern.MatchString(t.ID) || t.DeployID <= 0 || t.AgentID == "" || t.Host == "" || t.Service == "" || t.Environment == "" {
		return fmt.Errorf("invalid deployment task identity")
	}
	if !filepath.IsAbs(t.Root) || filepath.Clean(t.Root) != t.Root || t.Root == string(filepath.Separator) {
		return fmt.Errorf("invalid deployment root")
	}
	if !digestPattern.MatchString(t.ArtifactDigest) || t.ArtifactSize <= 0 || t.ArtifactSize > 16<<30 || (t.Bundle == nil && t.ArtifactSize > 4<<30) {
		return fmt.Errorf("invalid frozen artifact")
	}
	if len(t.Steps) == 0 || len(t.Steps) > 16 || t.Probe == "" || len(t.Probe) > 64<<10 || t.WarnAfterSeconds < 1 || t.WarnAfterSeconds > 86400 {
		return fmt.Errorf("invalid execution plan")
	}
	if t.Version == 1 && (t.Bundle != nil || t.Rollback != nil) {
		return fmt.Errorf("extended task requires protocol v2")
	}
	if t.Bundle != nil {
		if e := t.Bundle.Validate(); e != nil {
			return e
		}
	}
	if t.Rollback != nil {
		if t.Rollback.Guard == "" || len(t.Rollback.Guard) > 128<<10 || t.Rollback.Probe == "" || len(t.Rollback.Probe) > 128<<10 || len(t.Rollback.Steps) != 1 {
			return fmt.Errorf("invalid frozen rollback plan")
		}
		for _, step := range t.Rollback.Steps {
			if step.Script == "" || len(step.Script) > 128<<10 || step.RollbackOnExit {
				return fmt.Errorf("invalid rollback step")
			}
		}
	}
	for _, s := range t.Steps {
		if t.Version == 1 && (s.SyncRelease || s.RollbackOnExit) {
			return fmt.Errorf("extended step requires protocol v2")
		}
		if s.Name == "" || s.Script == "" || len(s.Script) > 128<<10 || strings.ContainsRune(s.Script, 0) {
			return fmt.Errorf("invalid execution step")
		}
	}
	for name, body := range t.Files {
		if filepath.IsAbs(name) || filepath.Clean(name) != name || name == "." || strings.HasPrefix(name, "..") || strings.Contains(name, "\\") || len(body) > 256<<10 {
			return fmt.Errorf("invalid task file")
		}
	}
	return nil
}
