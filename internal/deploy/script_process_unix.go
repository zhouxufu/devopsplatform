//go:build !windows

package deploy

import "syscall"

func scriptProcessGroupAlive(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || err == syscall.EPERM
}
