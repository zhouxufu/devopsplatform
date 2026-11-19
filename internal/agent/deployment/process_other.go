//go:build !linux && !darwin

package deployment

import (
	"fmt"
	"os"
)

func lock(string) (*os.File, error) {
	return nil, fmt.Errorf("deployment module is unsupported on this platform")
}
func processStart(int) string                 { return "" }
func alive(workerReceipt, string) bool        { return false }
func startWorker(string, Config) error        { return fmt.Errorf("deployment module requires Linux systemd") }
func diskAvailable(string) (uint64, error)    { return 0, fmt.Errorf("deployment module requires Linux") }
func currentUID() int                         { return -1 }
func isolatedWorkerGroup(string, Config) bool { return false }
func launchEnrolledWorker(string, Config, bool) error {
	return fmt.Errorf("deployment enrollment requires Linux systemd")
}
