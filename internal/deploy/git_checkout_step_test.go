package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCheckoutStepPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "steps", "git-checkout", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGitCheckoutExplainsSSHAndPATMismatch(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	cmd := exec.Command("bash", gitCheckoutStepPath(t))
	cmd.Env = append(os.Environ(),
		"LP_GIT_URL=git@codeup.aliyun.com:team/app.git",
		"LP_BRANCH=main",
		"LP_WORKSPACE="+workspace,
		"LP_OUTPUTS_FILE="+filepath.Join(t.TempDir(), "outputs"),
		"GIT_USERNAME=builder",
		"GIT_PASSWORD=token",
	)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "认证方式不匹配") || !strings.Contains(string(out), "HTTPS 用户名/PAT") {
		t.Fatalf("unexpected result err=%v output=%s", err, out)
	}
}

func TestGitCheckoutExplainsUnknownSSHHostKey(t *testing.T) {
	bin := t.TempDir()
	fakeGit := filepath.Join(bin, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\necho 'Host key verification failed.' >&2\necho 'fatal: Could not read from remote repository.' >&2\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	cmd := exec.Command("bash", gitCheckoutStepPath(t))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"LP_GIT_URL=git@codeup.aliyun.com:team/app.git",
		"LP_BRANCH=main",
		"LP_WORKSPACE="+workspace,
		"LP_OUTPUTS_FILE="+filepath.Join(t.TempDir(), "outputs"),
		"GIT_USERNAME=",
		"GIT_PASSWORD=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "主机指纹校验失败") || !strings.Contains(string(out), "获取并确认服务器指纹") {
		t.Fatalf("unexpected result err=%v output=%s", err, out)
	}
}

func TestGitCheckoutUsesTemporarySSHCredentialAndCleansIt(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ssh-material")
	fakeGit := filepath.Join(bin, "git")
	script := `#!/bin/bash
set -eu
if [ "${1:-}" = "clone" ]; then
  test -x "$GIT_SSH"
  test -z "${GIT_SSH_COMMAND:-}"
  grep -q "HostKeyAlgorithms=" "$GIT_SSH"
  grep -q "CheckHostIP=no" "$GIT_SSH"
  test "$DEVOPS_GIT_SSH_HOST_KEY_ALGORITHMS" = "ssh-ed25519"
  test "$DEVOPS_GIT_SSH_USER" = "git"
  key="$DEVOPS_GIT_SSH_KEY_FILE"
  known="$DEVOPS_GIT_SSH_KNOWN_HOSTS_FILE"
  test -f "$key"
  test -f "$known"
  grep -q "PRIVATE KEY" "$key"
  grep -q "ssh-ed25519" "$known"
  printf '%s\n' "$(dirname "$key")" > "$TEST_MARKER"
  mkdir -p "$LP_WORKSPACE/.git"
  exit 0
fi
echo abc123
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	cmd := exec.Command("bash", gitCheckoutStepPath(t))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"LP_GIT_URL=git@codeup.aliyun.com:team/app.git",
		"LP_BRANCH=main",
		"LP_WORKSPACE="+workspace,
		"LP_OUTPUTS_FILE="+filepath.Join(t.TempDir(), "outputs"),
		"GIT_USERNAME=git",
		"GIT_PASSWORD=",
		"GIT_SSH_PRIVATE_KEY=-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----",
		"GIT_SSH_KNOWN_HOSTS=codeup.aliyun.com ssh-ed25519 AAAATEST",
		"TEST_MARKER="+marker,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout failed: %v\n%s", err, out)
	}
	materialDir, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(materialDir))); !os.IsNotExist(err) {
		t.Fatalf("temporary SSH material was not removed: %v", err)
	}
}

func TestGitCheckoutIncrementalWorksWithoutGitCAndRefreshesOrigin(t *testing.T) {
	bin := t.TempDir()
	commands := filepath.Join(t.TempDir(), "git-commands")
	fakeGit := filepath.Join(bin, "git")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$TEST_COMMANDS"
case "$*" in
  *"rev-parse --short HEAD"*) echo abc123 ;;
esac
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", gitCheckoutStepPath(t))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"LP_GIT_URL=https://codeup.aliyun.com/team/app.git",
		"LP_BRANCH=main",
		"LP_WORKSPACE="+workspace,
		"LP_OUTPUTS_FILE="+filepath.Join(t.TempDir(), "outputs"),
		"GIT_USERNAME=builder",
		"GIT_PASSWORD=token",
		"TEST_COMMANDS="+commands,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	log := string(got)
	if strings.Contains(log, " -C ") {
		t.Fatalf("incremental checkout still uses unsupported git -C: %s", log)
	}
	for _, want := range []string{
		"remote set-url origin https://codeup.aliyun.com/team/app.git",
		"fetch --all --prune",
		"reset --hard origin/main",
		"clean -fd",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q in git commands:\n%s", want, log)
		}
	}
}
