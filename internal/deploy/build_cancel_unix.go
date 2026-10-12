//go:build !windows

package deploy

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureBuildCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			// Give shell EXIT traps a chance to remove temporary credentials.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	}
	cmd.WaitDelay = 2 * time.Second
}
func finishBuildCancellation(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
