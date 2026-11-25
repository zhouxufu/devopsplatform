package deployment

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"logplatform/pkg/deploytask"
)

type tailWriter struct{ data []byte }

func (w *tailWriter) Write(b []byte) (int, error) {
	n := len(b)
	if len(b) >= 32768 {
		w.data = append(w.data[:0], b[len(b)-32768:]...)
	} else {
		w.data = append(w.data, b...)
		if len(w.data) > 32768 {
			w.data = append([]byte{}, w.data[len(w.data)-32768:]...)
		}
	}
	return n, nil
}
func scrub(j job, s string) string {
	for _, v := range append(append([]string{}, j.Task.Secrets...), j.LocalSecrets...) {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	s = regexp.MustCompile(`(?i)((?:password|passwd|secret|token|authorization)[a-z0-9_-]*\s*[:=]\s*["']?)([^\s,"';}]+)`).ReplaceAllString(s, "${1}[REDACTED]")
	s = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]+`).ReplaceAllString(s, "${1}[REDACTED]")
	return s
}

func cleanEnv(dir string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "LANG=C", "LC_ALL=C", "DEPLOY_TASK_DIR=" + dir, "DEPLOY_ARTIFACT=" + filepath.Join(dir, "artifact")}
}

func runScript(ctx context.Context, dir, script string) (int, string) {
	cmd := exec.CommandContext(ctx, "bash", "-euo", "pipefail", "-c", script)
	cmd.Env = cleanEnv(dir)
	cmd.Dir = dir
	w := &tailWriter{}
	cmd.Stdout, cmd.Stderr = w, w
	e := cmd.Run()
	if e == nil {
		return 0, string(w.data)
	}
	if exit, ok := e.(*exec.ExitError); ok {
		return exit.ExitCode(), string(w.data)
	}
	return -1, string(w.data)
}

func releaseRoot(j job) error {
	dir := filepath.Join(j.Task.Root, ".deploy-lock")
	b, e := os.ReadFile(filepath.Join(dir, "owner"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(b)) != "agent:"+j.Task.ID {
		return nil
	} // Never touch a subsequent task's reservation.
	if e = os.Remove(filepath.Join(dir, "owner")); e != nil {
		return e
	}
	_ = os.Remove(filepath.Join(dir, "started_at"))
	return os.Remove(dir)
}

// RunWorker is an internal mode of the existing Agent executable, launched into
// its own systemd service. Its job file contains no Agent/network credential.
func RunWorker(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("invalid worker state directory")
	}
	f, e := lock(filepath.Join(dir, "execution.lock"))
	if e != nil {
		return e
	}
	defer f.Close()
	var j job
	if e = readJSON(filepath.Join(dir, "job.json"), &j); e != nil {
		return e
	}
	if j.Task.Validate() != nil || deploytask.Digest(j.Task) != j.Digest {
		return fmt.Errorf("frozen deployment plan changed")
	}
	if !isolatedWorkerGroup(dir, j.Policy) {
		return fmt.Errorf("deployment worker is not in its independent service cgroup")
	}
	if e = validateRoot(j.Task.Root, j.Policy.AllowedRoots); e != nil {
		return e
	}
	if _, e = os.Lstat(filepath.Join(dir, "intent")); e != nil {
		return fmt.Errorf("worker has no durable dispatch intent")
	}
	if _, e = os.Lstat(filepath.Join(dir, "worker.json")); !os.IsNotExist(e) {
		return fmt.Errorf("worker already executed; reconcile without replay")
	}
	if e = atomicJSON(filepath.Join(dir, "worker.json"), workerReceipt{os.Getpid(), processStart(os.Getpid())}); e != nil {
		return e
	}
	if e = os.MkdirAll(j.Task.Root, 0700); e != nil {
		return e
	}
	for _, name := range []string{"releases", "logs", ".devopsplatform.pid", ".devopsplatform-owner", ".deploy-lock"} {
		if e = validateRoot(filepath.Join(j.Task.Root, name), j.Policy.AllowedRoots); e != nil {
			return appendReport(dir, j, "needs_review", "layout", "Managed deployment layout crosses a symbolic link; no application changed")
		}
	}
	rootLock := filepath.Join(j.Task.Root, ".deploy-lock")
	if e = os.Mkdir(rootLock, 0700); e != nil {
		return appendReport(dir, j, "needs_review", "reservation", "Deployment directory is already reserved; no action was replayed")
	}
	if e = durableFile(filepath.Join(rootLock, "owner"), []byte("agent:"+j.Task.ID)); e != nil {
		return e
	}
	if e = durableFile(filepath.Join(rootLock, "started_at"), []byte(fmt.Sprint(time.Now().Unix()))); e != nil {
		return e
	}
	if e = syncDir(rootLock); e != nil {
		return e
	}
	if e = syncDir(j.Task.Root); e != nil {
		return e
	}
	required := j.Task.ArtifactSize + (64 << 20)
	if j.Task.Bundle != nil {
		required = j.Task.Bundle.ExpandedSize + (64 << 20)
	}
	free, e := diskAvailable(j.Task.Root)
	if e != nil {
		return e
	}
	if free < uint64(required) {
		return knownFailure(dir, j, false, "space", "Insufficient target filesystem space; old application was not stopped")
	}
	// Validate the downloaded payload again at the side-effect boundary.
	if e = verifyArtifact(filepath.Join(dir, "artifact"), j.Task); e != nil {
		_ = appendReport(dir, j, "failed", "artifact", "Frozen artifact verification failed")
		return releaseRoot(j)
	}
	if j.Task.Bundle != nil {
		if e = deploytask.ExtractBundle(filepath.Join(dir, "artifact"), filepath.Join(dir, "unpacked"), j.Task.Service, *j.Task.Bundle); e != nil {
			return knownFailure(dir, j, false, "bundle-verification", "Complete package verification failed before stopping any application")
		}
	}
	if j.Task.Version >= 2 {
		if e = capturePrevious(dir, j); e != nil {
			return appendReport(dir, j, "needs_review", "previous-version", scrub(j, e.Error()))
		}
		var previous previousVersion
		if e = readJSON(filepath.Join(dir, "previous.json"), &previous); e != nil {
			return e
		}
		message := "First managed publication; no previous version"
		if !previous.Empty {
			message = "Previous version frozen: " + filepath.Base(previous.Path) + " / " + previous.Digest
		}
		if e = appendReport(dir, j, "executing", "previous-version", message); e != nil {
			return e
		}
	}
	j = withPreviousSecrets(dir, j)
	for i, step := range j.Task.Steps {
		if e = validateRoot(j.Task.Root, j.Policy.AllowedRoots); e != nil {
			return e
		}
		if e = appendReport(dir, j, "executing", fmt.Sprintf("step-%d", i), step.Name); e != nil {
			return e
		}
		// This command may be slow. It is never killed merely for exceeding the
		// startup observation window; the manager can independently warn/reconcile.
		code, out := runScript(context.Background(), dir, step.Script)
		if code != 0 {
			state := "failed"
			if step.UncertainOnError && code != 20 {
				state = "needs_review"
			}
			if state == "failed" && code == 20 && step.RollbackOnExit {
				return knownFailure(dir, j, true, fmt.Sprintf("step-%d", i), scrub(j, out))
			}
			if e = appendReport(dir, j, state, fmt.Sprintf("step-%d", i), scrub(j, fmt.Sprintf("%s exited %d\n%s", step.Name, code, out))); e != nil {
				return e
			}
			if state == "failed" {
				return releaseRoot(j)
			}
			return nil
		}
		if step.SyncRelease {
			if e = syncVersion(filepath.Join(j.Task.Root, "releases", fmt.Sprintf("deploy-%d", j.Task.DeployID))); e != nil {
				return knownFailure(dir, j, false, "sync-before-switch", "New version could not be synchronized; old application was not stopped")
			}
			_ = os.Remove(filepath.Join(dir, "artifact"))
			_ = os.RemoveAll(filepath.Join(dir, "unpacked"))
		}
	}
	if e = appendReport(dir, j, "starting", "verifying", "Waiting for configured process/readiness checks"); e != nil {
		return e
	}
	// The immutable release now owns its payload. The controller's duplicate
	// download is no longer needed for observation/recovery; receipts stay.
	_ = os.Remove(filepath.Join(dir, "artifact"))
	_ = os.Remove(filepath.Join(dir, "artifact.part"))
	return observe(dir, j, false)
}

// Reconcile performs observation only. Once dispatch intent exists, restarting
// the Agent must never replay a step whose side effects may already have run.
func reconcile(dir string, j job) error {
	j = withPreviousSecrets(dir, j)
	f, e := lock(filepath.Join(dir, "execution.lock"))
	if e != nil {
		return nil
	}
	defer f.Close()
	r, e := latest(dir)
	if e != nil {
		return e
	}
	if deploytask.Terminal(r.State) {
		return releaseRoot(j)
	}
	var w workerReceipt
	if readJSON(filepath.Join(dir, "worker.json"), &w) == nil && alive(w, dir) {
		return nil
	}
	var rollback rollbackReceipt
	if e := readJSON(filepath.Join(dir, "rollback.json"), &rollback); e == nil {
		if j.Task.Rollback == nil {
			return fmt.Errorf("rollback receipt without a frozen plan")
		}
		code, out := rollbackProbe(dir, j)
		if code == 0 {
			if e = appendReport(dir, j, "rolled-back", "verified-rollback-after-recovery", scrub(j, out)); e != nil {
				return e
			}
			return releaseRoot(j)
		}
		if code == 20 {
			if e = appendReport(dir, j, "rollback-failed", "rollback-process-exited", scrub(j, out)); e != nil {
				return e
			}
			return releaseRoot(j)
		}
		return appendReport(dir, j, "needs_review", "rollback-recovery", "Rollback was interrupted; original previous version is observed without replay or stop")
	} else if !os.IsNotExist(e) {
		return e
	}
	{
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		code, out := runScript(ctx, dir, j.Task.Probe)
		if code == 0 {
			if e = appendReport(dir, j, "success", "verified-after-recovery", scrub(j, out)); e != nil {
				return e
			}
			return releaseRoot(j)
		}
		if code == 20 {
			if j.Task.Rollback != nil {
				return appendReport(dir, j, "needs_review", "rollback-not-confirmed", "Process exit verified after worker interruption; compensation dispatch was not confirmed and will not be replayed")
			}
			if e = appendReport(dir, j, "failed", "process-exited", scrub(j, out)); e != nil {
				return e
			}
			return releaseRoot(j)
		}
		if code == 10 {
			return appendReport(dir, j, "needs_review", "verifying", "Worker was interrupted; process/readiness observation continues without replay or stop")
		}
	}
	return appendReport(dir, j, "needs_review", r.Phase, "Execution may have started before interruption; no deployment step will be replayed")
}
