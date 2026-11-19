// Package deployment is the optional, independently authorized Agent task module.
package deployment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"logplatform/pkg/deploytask"
)

type Config struct {
	Enabled      bool     `yaml:"enabled" json:"enabled"`
	URL          string   `yaml:"url" json:"url"`
	Token        string   `yaml:"token" json:"-"`
	StateDir     string   `yaml:"state_dir" json:"state_dir"`
	AllowedRoots []string `yaml:"allowed_roots" json:"allowed_roots"`
	// systemd separates applications from the log Agent's memory/cgroup limits.
	WorkerMode     string `yaml:"worker_mode,omitempty" json:"worker_mode"`
	WorkerLauncher string `yaml:"worker_launcher,omitempty" json:"worker_launcher,omitempty"`
	EnrollmentID   string `yaml:"enrollment_id,omitempty" json:"enrollment_id,omitempty"`
	MaxParallel    int    `yaml:"max_parallel,omitempty" json:"max_parallel"`
	AgentID        string `yaml:"-" json:"agent_id"`
	Host           string `yaml:"-" json:"host"`
	Environment    string `yaml:"-" json:"environment"`
}

type job struct {
	LocalSecrets []string        `json:"-"`
	Task         deploytask.Task `json:"task"`
	Digest       string          `json:"digest"`
	Policy       Config          `json:"policy"`
}
type workerReceipt struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func atomicJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".state-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func readJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if len(b) > 2<<20 {
		return fmt.Errorf("oversized deployment state")
	}
	return json.Unmarshal(b, v)
}

func validateRoot(root string, allowed []string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return fmt.Errorf("invalid deployment root")
	}
	for _, base := range allowed {
		if !filepath.IsAbs(base) || filepath.Clean(base) != base || base == "/" {
			continue
		}
		rel, e := filepath.Rel(base, root)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		// Never traverse a link below the explicit local policy boundary.
		probe := base
		for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
			if part != "" && part != "." {
				probe = filepath.Join(probe, part)
			}
			info, e := os.Lstat(probe)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("deployment root traverses a symlink")
			}
		}
		return nil
	}
	return fmt.Errorf("deployment root is outside local policy")
}

func reports(dir string) ([]deploytask.Report, error) {
	entries, e := os.ReadDir(filepath.Join(dir, "outbox"))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if len(entries) > 4096 {
		return nil, fmt.Errorf("deployment report journal limit reached")
	}
	out := []deploytask.Report{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var r deploytask.Report
		if e = readJSON(filepath.Join(dir, "outbox", entry.Name()), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	for i, r := range out {
		if r.Sequence != int64(i+1) {
			return nil, fmt.Errorf("broken deployment report sequence")
		}
	}
	return out, nil
}

func appendReport(dir string, j job, state, phase, message string) error {
	all, e := reports(dir)
	if e != nil {
		return e
	}
	if len(all) > 0 {
		prior := all[len(all)-1]
		if prior.State == state && prior.Phase == phase && prior.Message == message {
			return nil
		}
		if deploytask.Terminal(prior.State) {
			return fmt.Errorf("deployment already terminal")
		}
	}
	if len(all) >= 4096 {
		return fmt.Errorf("deployment report journal is full")
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	r := deploytask.Report{TaskID: j.Task.ID, TaskDigest: j.Digest, Sequence: int64(len(all) + 1), State: state, Phase: phase, Message: message, At: time.Now().Unix()}
	return atomicJSON(filepath.Join(dir, "outbox", fmt.Sprintf("%020d.json", r.Sequence)), r)
}

func latest(dir string) (deploytask.Report, error) {
	all, e := reports(dir)
	if e != nil {
		return deploytask.Report{}, e
	}
	if len(all) == 0 {
		return deploytask.Report{}, nil
	}
	return all[len(all)-1], nil
}

func durableFile(path string, body []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(body)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}
