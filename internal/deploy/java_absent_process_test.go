package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedJavaMissingPIDNeedsCompleteProcessEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       int
	}{
		{"foreign-port-owner", "java\x00-jar\x00/old/jpom/app.jar\x00", 20},
		{"unreceipted-jvm", "java\x00-jar\x00APP_JAR\x00", 31},
		{"pending-launcher", "bash\x00LAUNCHER\x00start\x00", 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			release := root + "/releases/deploy-41"
			proc := root + "/proc"
			dir := proc + "/123"
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			args := strings.ReplaceAll(strings.ReplaceAll(tc.args, "APP_JAR", release+"/app.jar"), "LAUNCHER", release+"/service.sh")
			if e := os.WriteFile(dir+"/cmdline", []byte(args), 0600); e != nil {
				t.Fatal(e)
			}
			command := "base=" + shellQuote(root) + "; " + managedJavaAbsentProcessProbe(root, release, proc)
			out, e := exec.Command("bash", "-c", command).CombinedOutput()
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ExitCode() != tc.want {
				t.Fatalf("%v: %s", e, out)
			}
			// PID files still take the original frozen-launcher probe path.
			if e := os.WriteFile(filepath.Join(root, ".devopsplatform.pid"), []byte("123"), 0600); e != nil {
				t.Fatal(e)
			}
			if out, e = exec.Command("bash", "-c", command+"echo original-probe").CombinedOutput(); e != nil || strings.TrimSpace(string(out)) != "original-probe" {
				t.Fatalf("PID evidence bypassed: %v %s", e, out)
			}
		})
	}
	t.Run("unreadable-process-directory", func(t *testing.T) {
		root := t.TempDir()
		command := "base=" + shellQuote(root) + "; " + managedJavaAbsentProcessProbe(root, root+"/release", root+"/missing")
		e := exec.Command("bash", "-c", command).Run()
		if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 31 {
			t.Fatal("missing process evidence cleared protection", e)
		}
	})
}
