//go:build windows

package deploy

// Windows does not provide the POSIX execution contract used by Bash publishing.
func scriptProcessGroupAlive(pid int) bool { return true }
