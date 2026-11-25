package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type previousVersion struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Empty  bool   `json:"empty"`
}
type rollbackReceipt struct {
	Phase string `json:"phase"`
}

// Hash the frozen previous files, including its launcher and environment. Reject
// links/devices; rollback must not follow a mutable external version tree.
func versionDigest(root string) (string, error) {
	entries := []string{}
	var size int64
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return fmt.Errorf("previous version contains an unsupported member")
		}
		if len(entries) > 100000 {
			return fmt.Errorf("previous version has too many files")
		}
		entries = append(entries, p)
		size += info.Size()
		if size > 17<<30 {
			return fmt.Errorf("previous version too large")
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	sort.Strings(entries)
	hash := sha256.New()
	for _, p := range entries {
		info, e := os.Stat(p)
		if e != nil {
			return "", e
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", rel, info.Mode().Perm(), info.Size())
		if info.IsDir() {
			continue
		}
		f, e := os.Open(p)
		if e != nil {
			return "", e
		}
		_, e = io.Copy(hash, f)
		f.Close()
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func capturePrevious(dir string, j job) error {
	var previous previousVersion
	owner, e := os.ReadFile(filepath.Join(j.Task.Root, ".devopsplatform-owner"))
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e == nil && string(owner) != j.Task.Service+"/"+j.Task.Environment {
		return fmt.Errorf("deployment directory owner mismatch")
	}
	current := filepath.Join(j.Task.Root, "current")
	p, e := os.Readlink(current)
	if os.IsNotExist(e) {
		previous.Empty = true
	} else {
		if e != nil || len(owner) == 0 {
			return fmt.Errorf("current version cannot be verified")
		}
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || filepath.Dir(p) != filepath.Join(j.Task.Root, "releases") || !strings.HasPrefix(filepath.Base(p), "deploy-") {
			return fmt.Errorf("previous version is outside managed releases")
		}
		if e = validateRoot(p, j.Policy.AllowedRoots); e != nil {
			return e
		}
		previous.Path = p
		if _, e = os.Stat(filepath.Join(p, "service.sh")); e != nil {
			return e
		}
		if j.Task.Rollback != nil {
			previous.Digest, e = versionDigest(p)
			if e != nil {
				return e
			}
		}
	}
	if e = atomicJSON(filepath.Join(dir, "previous.json"), previous); e != nil {
		return e
	}
	return durableFile(filepath.Join(dir, "previous"), []byte(previous.Path))
}
func verifiedPrevious(dir string, j job) (previousVersion, error) {
	var p previousVersion
	if e := readJSON(filepath.Join(dir, "previous.json"), &p); e != nil {
		return p, e
	}
	if p.Empty {
		return p, nil
	}
	if p.Path == "" || p.Digest == "" || filepath.Dir(p.Path) != filepath.Join(j.Task.Root, "releases") {
		return p, fmt.Errorf("previous version identity missing")
	}
	if e := validateRoot(p.Path, j.Policy.AllowedRoots); e != nil {
		return p, e
	}
	got, e := versionDigest(p.Path)
	if e != nil {
		return p, e
	}
	if got != p.Digest {
		return p, fmt.Errorf("previous version changed since publication")
	}
	return p, nil
}
func rollbackProbe(dir string, j job) (int, string) {
	if _, e := verifiedPrevious(dir, j); e != nil {
		return 30, "Frozen previous version cannot be verified"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runScript(ctx, dir, j.Task.Rollback.Probe)
}
func beginRollback(dir string, j job, message string) error {
	p, e := verifiedPrevious(dir, j)
	if e != nil {
		return appendReport(dir, j, "needs_review", "rollback-guard", "Previous code/configuration cannot be verified; no rollback executed")
	}
	if p.Empty {
		if e = appendReport(dir, j, "failed", "no-previous", "First publication failed; no previous version exists, no process was stopped"); e != nil {
			return e
		}
		return releaseRoot(j)
	}
	// Durable intent precedes the compensation. Recovery only observes it; it
	// never repeats a potentially completed switch or application start.
	if e = atomicJSON(filepath.Join(dir, "rollback.json"), rollbackReceipt{Phase: "guard"}); e != nil {
		return e
	}
	if e = appendReport(dir, j, "rolling_back", "rollback-guard", message); e != nil {
		return e
	}
	code, out := runScript(context.Background(), dir, j.Task.Rollback.Guard)
	if code != 0 {
		return appendReport(dir, j, "needs_review", "rollback-guard", scrub(j, "Failure/ownership evidence changed; no rollback action taken\n"+out))
	}
	for i, step := range j.Task.Rollback.Steps {
		phase := fmt.Sprintf("rollback-step-%d", i)
		if e = atomicJSON(filepath.Join(dir, "rollback.json"), rollbackReceipt{phase}); e != nil {
			return e
		}
		if e = appendReport(dir, j, "rolling_back", phase, step.Name); e != nil {
			return e
		}
		code, out = runScript(context.Background(), dir, step.Script)
		if code != 0 {
			probe, _ := rollbackProbe(dir, j)
			if probe == 20 {
				if e = appendReport(dir, j, "rollback-failed", "rollback-process-exited", scrub(j, out)); e != nil {
					return e
				}
				return releaseRoot(j)
			}
			return appendReport(dir, j, "needs_review", phase, scrub(j, "Rollback outcome is unknown; no action will be replayed\n"+out))
		}
	}
	if e = atomicJSON(filepath.Join(dir, "rollback.json"), rollbackReceipt{"verifying"}); e != nil {
		return e
	}
	return observe(dir, j, true)
}
func knownFailure(dir string, j job, allowRollback bool, phase, message string) error {
	if allowRollback && j.Task.Rollback != nil {
		return beginRollback(dir, j, message)
	}
	if e := appendReport(dir, j, "failed", phase, message); e != nil {
		return e
	}
	return releaseRoot(j)
}
func observe(dir string, j job, rollback bool) error {
	start := time.Now()
	warned := false
	probeScript := j.Task.Probe
	success := "success"
	failure := "failed"
	phase := "verified"
	if rollback {
		probeScript = j.Task.Rollback.Probe
		success = "rolled-back"
		failure = "rollback-failed"
		phase = "verified-rollback"
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		code, out := runScript(ctx, dir, probeScript)
		cancel()
		switch code {
		case 0:
			if e := appendReport(dir, j, success, phase, scrub(j, out)); e != nil {
				return e
			}
			return releaseRoot(j)
		case 21:
			if !rollback {
				return knownFailure(dir, j, false, "start-rejected", scrub(j, out))
			}
			return appendReport(dir, j, "needs_review", "rollback-start-rejected", scrub(j, out))
		case 20:
			if !rollback {
				return knownFailure(dir, j, true, "process-exited", scrub(j, out))
			}
			if e := appendReport(dir, j, failure, "rollback-process-exited", scrub(j, out)); e != nil {
				return e
			}
			return releaseRoot(j)
		default:
			if !warned && (code != 10 || time.Since(start) >= time.Duration(j.Task.WarnAfterSeconds)*time.Second) {
				warned = true
				which := "verifying"
				if rollback {
					which = "rollback-verifying"
				}
				if e := appendReport(dir, j, "needs_review", which, scrub(j, "Readiness remains unverified; process retained, no stop or replay\n"+out)); e != nil {
					return e
				}
			}
		}
		time.Sleep(time.Second)
	}
}

func syncVersion(root string) error {
	dirs := []string{}
	count := 0
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > 100010 {
			return fmt.Errorf("version file limit exceeded")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("version contains link")
		}
		if info.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported version file")
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		e = f.Sync()
		f.Close()
		return e
	})
	if e != nil {
		return e
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if e = syncDir(dirs[i]); e != nil {
			return e
		}
	}
	return syncDir(filepath.Dir(root))
}

func withPreviousSecrets(dir string, j job) job {
	var p previousVersion
	if readJSON(filepath.Join(dir, "previous.json"), &p) != nil || p.Empty || filepath.Dir(p.Path) != filepath.Join(j.Task.Root, "releases") {
		return j
	}
	if validateRoot(p.Path, j.Policy.AllowedRoots) != nil {
		return j
	}
	file, e := os.Open(filepath.Join(p.Path, "runtime.env"))
	if e != nil {
		return j
	}
	b, e := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	file.Close()
	if e != nil || len(b) > 1<<20 {
		return j
	}
	for _, line := range strings.Split(string(b), "\n") {
		_, value, ok := strings.Cut(line, "=")
		if !ok || value == "" {
			continue
		}
		if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			value = strings.ReplaceAll(value[1:len(value)-1], `'\''`, "'")
		}
		if value != "" {
			j.LocalSecrets = append(j.LocalSecrets, value)
		}
	}
	return j
}
