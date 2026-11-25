//go:build linux || darwin

package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func lock(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func processStart(pid int) string {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return ""
	}
	end := strings.LastIndex(string(b), ") ")
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(b[end+2:]))
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

func diskAvailable(dir string) (uint64, error) {
	var s unix.Statfs_t
	if e := unix.Statfs(dir, &s); e != nil {
		return 0, e
	}
	return uint64(s.Bavail) * uint64(s.Bsize), nil
}

func currentUID() int { return os.Geteuid() }

func unitName(dir string, c Config) string {
	sum := sha256.Sum256([]byte(c.StateDir))
	return "devops-deploy-" + hex.EncodeToString(sum[:])[:12] + "-" + filepath.Base(dir) + ".service"
}
func isolatedWorkerGroup(dir string, c Config) bool {
	if c.WorkerMode == "isolated-process" {
		return true
	}
	b, e := os.ReadFile("/proc/self/cgroup")
	return e == nil && strings.Contains(string(b), unitName(dir, c))
}
func alive(r workerReceipt, dir string) bool {
	if r.PID <= 0 || syscall.Kill(r.PID, 0) != nil {
		return false
	}
	if runtime.GOOS != "linux" {
		b, e := exec.Command("ps", "-p", strconv.Itoa(r.PID), "-o", "command=").Output()
		return e == nil && strings.Contains(string(b), "-deployment-worker "+dir)
	} // Isolated macOS tests only; production uses Linux.
	if r.Start == "" || processStart(r.PID) != r.Start {
		return false
	}
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", r.PID))
	return e == nil && strings.Contains(string(b), "-deployment-worker\x00"+dir+"\x00")
}
func startWorker(dir string, c Config) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	args := []string{"-deployment-worker", dir}
	if c.WorkerMode == "isolated-process" {
		cmd := exec.Command(exe, args...)
		cmd.Env = cleanEnv(dir)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		f, e := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		cmd.Stdout, cmd.Stderr = f, f
		if e = cmd.Start(); e != nil {
			return e
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("production deployment workers require Linux systemd")
	}
	if c.WorkerLauncher != "" {
		return launchEnrolledWorker(dir, c, false)
	}
	// Use systemd-run 219 flags for service execution options, not newer
	// transient property assignments. Restart defaults to no.
	unit := unitName(dir, c)
	params := []string{"--service-type=simple", "--unit=" + unit, "--property=KillMode=process", "--uid=" + strconv.Itoa(os.Geteuid()), "--gid=" + strconv.Itoa(os.Getegid()), exe}
	params = append(params, args...)
	cmd := exec.Command("systemd-run", params...)
	cmd.Env = cleanEnv(dir)
	if b, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("independent deployment unit was not confirmed (%s); check local systemd authorization", strconv.Quote(string(b)))
	}
	return nil
}

func launchEnrolledWorker(dir string, c Config, check bool) error {
	if runtime.GOOS != "linux" || c.WorkerLauncher != "/usr/local/libexec/logagent-deployment-worker" {
		return fmt.Errorf("invalid fixed deployment launcher")
	}
	name := filepath.Base(dir)
	if check {
		name = "check-" + name
	} else if !strings.HasPrefix(name, "deploy-") || filepath.Dir(dir) != c.StateDir {
		return fmt.Errorf("invalid deployment worker path")
	}
	cmd := exec.Command("sudo", "-n", c.WorkerLauncher, name)
	if currentUID() == 0 {
		cmd = exec.Command(c.WorkerLauncher, name)
	}
	cmd.Env = cleanEnv(dir)
	if b, e := cmd.CombinedOutput(); e != nil {
		return fmt.Errorf("independent worker authorization was not confirmed: %s", strconv.Quote(string(b)))
	}
	return nil
}
