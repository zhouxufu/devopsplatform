package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Discovery runs on the platform, not in a build workspace. No checkout,
// branch creation or persistent credential-helper mutation is performed.
type GitBranchesRequest struct {
	Service    string  `json:"service"`
	GitURL     *string `json:"git_url,omitempty"`
	Connection *string `json:"connection,omitempty"`
}
type GitBranch struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}
type GitBranches struct {
	Branches      []GitBranch `json:"branches"`
	DefaultBranch string      `json:"default_branch"`
	SyncedAt      int64       `json:"synced_at"`
}

var scpGitURL = regexp.MustCompile(`^[A-Za-z0-9_.-]+@([A-Za-z0-9.-]+):[^\s]+$`)
var gitObjectID = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)
var gitDiscoverySlots = make(chan struct{}, 6)

func validateGitDiscoveryURL(raw string) error {
	if len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\x00\t ") {
		return fmt.Errorf("代码仓库地址无效")
	}
	if scpGitURL.MatchString(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Path == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("分支同步需要完整的 HTTPS 或 SSH 仓库地址，不支持本地目录")
	}
	if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" {
		return fmt.Errorf("分支同步仅支持 HTTP(S) 或 SSH 仓库")
	}
	if u.User != nil {
		_, password := u.User.Password()
		if u.Scheme != "ssh" || password {
			return fmt.Errorf("仓库地址不能包含密码或 Token，请选择资源中心的 Git 凭据")
		}
	}
	return nil
}

func (s *Store) gitDiscoverySource(r GitBranchesRequest) (string, *ResourceConnection, error) {
	var repository, connection string
	if r.Service != "" {
		sv := s.GetService(r.Service)
		if sv == nil {
			return "", nil, fmt.Errorf("应用不存在")
		}
		repository = sv.GitURL
		if d := s.GetServiceDelivery(r.Service); d != nil {
			connection = d.GitConnection
		}
	}
	if r.GitURL != nil {
		repository = *r.GitURL
	}
	if r.Connection != nil {
		connection = *r.Connection
	}
	repository = strings.TrimSpace(repository)
	if err := validateGitDiscoveryURL(repository); err != nil {
		return "", nil, err
	}
	if connection == "" {
		return repository, nil, nil
	}
	c := s.GetConnection(connection)
	if c == nil || c.Kind != "git" {
		return "", nil, fmt.Errorf("所选 Git 代码凭据不存在或类型不正确")
	}
	if connectionHost(c.Endpoint) != connectionHost(repository) {
		return "", nil, fmt.Errorf("Git 凭据与仓库主机不匹配，未发送凭据")
	}
	mode, err := normalizeGitAuthMode(c.Config["auth_mode"])
	if err != nil {
		return "", nil, err
	}
	if (mode == gitAuthSSH) != isGitSSHURL(repository) {
		return "", nil, fmt.Errorf("Git 凭据认证方式与仓库地址不一致，请统一使用 HTTPS 或 SSH")
	}
	if mode == gitAuthSSH {
		repoAddress := repository
		if !strings.HasPrefix(repository, "ssh://") {
			parts := scpGitURL.FindStringSubmatch(repository)
			if len(parts) < 2 {
				return "", nil, fmt.Errorf("SSH 仓库地址无效")
			}
			repoAddress = parts[1]
		} else {
			u, _ := url.Parse(repository)
			repoAddress = u.Host
		}
		repoAddress, err = normalizeGitSSHEndpoint(repoAddress)
		if err != nil || repoAddress != c.Endpoint {
			return "", nil, fmt.Errorf("Git SSH 凭据与仓库端口不匹配，未发送凭据")
		}
	} else {
		u, _ := url.Parse(repository)
		endpoint, _ := url.Parse(c.Endpoint)
		if endpoint == nil || u.Scheme != endpoint.Scheme || u.Host != endpoint.Host {
			return "", nil, fmt.Errorf("Git 凭据与仓库协议或端口不匹配，未发送凭据")
		}
	}
	return repository, c, nil
}

type gitOutputLimit struct{ bytes.Buffer }

func (b *gitOutputLimit) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2<<20 {
		return 0, fmt.Errorf("Git 分支响应过大")
	}
	return b.Buffer.Write(p)
}

func parseGitBranches(raw string) (*GitBranches, error) {
	out := &GitBranches{Branches: []GitBranch{}, SyncedAt: time.Now().Unix()}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" && strings.HasPrefix(fields[1], "refs/heads/") {
			out.DefaultBranch = strings.TrimPrefix(fields[1], "refs/heads/")
		}
		if len(fields) != 2 || !gitObjectID.MatchString(fields[0]) || !strings.HasPrefix(fields[1], "refs/heads/") {
			continue
		}
		name := strings.TrimPrefix(fields[1], "refs/heads/")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out.Branches = append(out.Branches, GitBranch{Name: name, Commit: fields[0]})
		if len(out.Branches) > 10000 {
			return nil, fmt.Errorf("远程分支超过 10000 个，请清理过期分支后再同步")
		}
	}
	sort.Slice(out.Branches, func(i, j int) bool { return out.Branches[i].Name < out.Branches[j].Name })
	return out, nil
}

func gitDiscoveryError(log string) error {
	log = strings.ToLower(log)
	message := "分支同步失败，请检查仓库地址、所选 Git 凭据和平台到仓库的网络"
	switch {
	case strings.Contains(log, "host key verification failed"):
		message = "Git SSH 主机指纹校验失败，请在资源中心获取并确认指纹"
	case strings.Contains(log, "permission denied (publickey)"):
		message = "Git SSH 私钥认证失败，请检查公钥授权及仓库读取权限"
	case strings.Contains(log, "authentication failed"), strings.Contains(log, "403"), strings.Contains(log, "401"):
		message = "Git HTTPS 认证失败，请检查用户名、PAT 和仓库读取权限"
	case strings.Contains(log, "could not resolve"), strings.Contains(log, "connection timed out"), strings.Contains(log, "failed to connect"):
		message = "平台无法连接代码仓库，请检查 DNS、网络或防火墙；同步在平台上执行，不在 Runner 上执行"
	case strings.Contains(log, "repository not found"), strings.Contains(log, "not appear to be a git repository"):
		message = "仓库不存在或当前凭据没有仓库读取权限"
	}
	// Never forward Git stderr: a server or credential helper may echo secrets.
	return fmt.Errorf("%s", message)
}

func discoverGitBranches(ctx context.Context, repository string, c *ResourceConnection) (*GitBranches, error) {
	session, err := newGitSession(repository, c)
	if err != nil {
		return nil, err
	}
	defer session.close()
	dir, env, args := session.dir, session.env, session.args
	args = append(args, "ls-remote", "--symref", "--", repository, "HEAD", "refs/heads/*")
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, env, 2*time.Second
	var stdout, stderr gitOutputLimit
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("分支同步超时或已取消，请检查平台到仓库的网络")
		}
		if _, ok := err.(*exec.Error); ok {
			return nil, fmt.Errorf("平台执行环境未安装 Git，无法同步分支")
		}
		return nil, gitDiscoveryError(stderr.String())
	}
	return parseGitBranches(stdout.String())
}

func registerGitBranchRoutes(mux *http.ServeMux, s *Store) {
	registerFeatureBranchRoutes(mux, s)
	reply := func(w http.ResponseWriter, value any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("POST /api/git/branches", func(w http.ResponseWriter, r *http.Request) {
		var request GitBranchesRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&request) != nil {
			reply(w, nil, fmt.Errorf("分支同步请求格式错误"))
			return
		}
		repository, c, err := s.gitDiscoverySource(request)
		if err != nil {
			reply(w, nil, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		select {
		case gitDiscoverySlots <- struct{}{}:
			defer func() { <-gitDiscoverySlots }()
		case <-ctx.Done():
			reply(w, nil, fmt.Errorf("分支同步请求繁忙，请稍后重试"))
			return
		}
		result, err := discoverGitBranches(ctx, repository, c)
		reply(w, result, err)
	})
}
