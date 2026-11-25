package deployment

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentStartRefusedClosesFailureWithoutRollback(t *testing.T) {
	for _, where := range []string{"step", "probe"} {
		t.Run(where, func(t *testing.T) {
			c, f := setupRollbackTask(t, false)
			f.task.Steps[0].Script = "echo 'port in use; start refused'; exit 21"
			if where == "probe" {
				f.task.Steps[0].Script = "true"
			}
			f.task.Probe = "echo 'persisted rejection'; exit 21"
			m, e := Open(c)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			if e = m.Tick(context.Background()); e != nil {
				t.Fatal(e)
			}
			if e = m.Tick(context.Background()); e != nil {
				t.Fatal(e)
			}
			waitFor(t, func() bool { return taskReport(c).State == "failed" })
			if _, e = os.Stat(filepath.Join(f.task.Root, "rollback-starts")); !os.IsNotExist(e) {
				t.Fatal("a refused launch dispatched rollback")
			}
			waitFor(t, func() bool { _, e := os.Stat(filepath.Join(f.task.Root, ".deploy-lock")); return os.IsNotExist(e) })
			if e = m.Tick(context.Background()); e != nil {
				t.Fatal(e)
			}
			if taskReport(c).State != "failed" {
				t.Fatal("terminal failure replayed")
			}
		})
	}
}
