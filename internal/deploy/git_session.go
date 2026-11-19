package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type gitSession struct {
	dir       string
	env, args []string
}

func (g *gitSession) close() { os.RemoveAll(g.dir) }
func newGitSession(repository string, c *ResourceConnection) (session *gitSession, err error) {
	dir, err := os.MkdirTemp("", "devopsplatform-git-branches-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if session == nil {
			os.RemoveAll(dir)
		}
	}()
	write := func(name, value string, mode os.FileMode) (string, error) {
		path := filepath.Join(dir, name)
		return path, os.WriteFile(path, []byte(value), mode)
	}
	env := []string{}
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		if !strings.HasPrefix(key, "GIT_") && !strings.HasPrefix(key, "DEVOPS_GIT_") && !strings.HasPrefix(key, "SSH_") {
			env = append(env, value)
		}
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	// Disable inherited Git helpers and redirects, so credentials cannot be sent
	// to another host via an HTTP redirect or a local credential helper.
	args := []string{"-c", "credential.helper=", "-c", "core.askPass=", "-c", "http.followRedirects=false"}
	if c != nil && c.Config["auth_mode"] == gitAuthSSH {
		if strings.TrimSpace(c.Config["known_hosts"]) == "" {
			return nil, fmt.Errorf("未确认 Git SSH 主机指纹")
		}
		key, e := write("id_key", c.PrivateKey+"\n", 0o600)
		if e != nil {
			return nil, e
		}
		known, e := write("known_hosts", c.Config["known_hosts"]+"\n", 0o600)
		if e != nil {
			return nil, e
		}
		fields := strings.Fields(c.Config["known_hosts"])
		if len(fields) < 3 {
			return nil, fmt.Errorf("Git 主机指纹配置无效")
		}
		algorithm := fields[1]
		if algorithm == "ssh-rsa" {
			algorithm = "rsa-sha2-512,rsa-sha2-256,ssh-rsa"
		}
		wrapper, e := write("ssh", "#!/bin/sh\nexec ssh -F /dev/null -i \"$DEVOPS_GIT_KEY\" -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o CheckHostIP=no -o ConnectTimeout=10 -o \"HostKeyAlgorithms=$DEVOPS_GIT_ALGORITHMS\" -o \"UserKnownHostsFile=$DEVOPS_GIT_KNOWN\" -l \"$DEVOPS_GIT_USER\" \"$@\"\n", 0o700)
		if e != nil {
			return nil, e
		}
		askpass, e := write("ssh-askpass", "#!/bin/sh\nprintf '%s\\n' \"$DEVOPS_GIT_PASSPHRASE\"\n", 0o700)
		if e != nil {
			return nil, e
		}
		user := c.Username
		if user == "" {
			user = "git"
		}
		env = append(env, "GIT_SSH="+wrapper, "DEVOPS_GIT_KEY="+key, "DEVOPS_GIT_KNOWN="+known, "DEVOPS_GIT_USER="+user, "DEVOPS_GIT_ALGORITHMS="+algorithm, "DEVOPS_GIT_PASSPHRASE="+c.Passphrase, "SSH_ASKPASS="+askpass, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=devopsplatform:0")
	} else if c != nil {
		askpass, e := write("askpass", "#!/bin/sh\ncase \"$1\" in *Username*) printf '%s\\n' \"$DEVOPS_GIT_USER\";; *) printf '%s\\n' \"$DEVOPS_GIT_PASSWORD\";; esac\n", 0o700)
		if e != nil {
			return nil, e
		}
		env = append(env, "GIT_ASKPASS="+askpass, "DEVOPS_GIT_USER="+c.Username, "DEVOPS_GIT_PASSWORD="+c.Password, "LC_ALL=C")
	} else if isGitSSHURL(repository) {
		// Read-only discovery may use the platform user's key and known_hosts,
		// but never prompt, trust an unknown host, or modify known_hosts.
		wrapper, e := write("ssh", "#!/bin/sh\nexec ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=10 \"$@\"\n", 0o700)
		if e != nil {
			return nil, e
		}
		env = append(env, "GIT_SSH="+wrapper)
	}

	args = append(args, "-c", "core.hooksPath="+os.DevNull, "-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=never")
	return &gitSession{dir: dir, env: env, args: args}, nil
}
func (g *gitSession) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append(append([]string{}, g.args...), args...)...)
	cmd.Dir = g.dir
	cmd.Env = g.env
	cmd.WaitDelay = 2 * time.Second
	var out, log gitOutputLimit
	cmd.Stdout = &out
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("Git 操作超时或已取消")
		}
		return "", gitDiscoveryError(log.String())
	}
	return strings.TrimSpace(out.String()), nil
}
