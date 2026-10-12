//go:build windows

package deploy

import (
	"os/exec"
	"time"
)

func configureBuildCancellation(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
func finishBuildCancellation(cmd *exec.Cmd)    {}
