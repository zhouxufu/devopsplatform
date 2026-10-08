#!/bin/bash
set -euo pipefail
URL="$LP_GIT_URL"; BR="${LP_BRANCH:-}"
ssh_material_dir=""

cleanup_checkout_secrets() {
  if [ -n "$ssh_material_dir" ] && [ -d "$ssh_material_dir" ]; then
    rm -rf -- "$ssh_material_dir"
  fi
}
trap cleanup_checkout_secrets EXIT

is_ssh_url() {
  [[ "$URL" == ssh://* || "$URL" =~ ^[^/@[:space:]]+@[^:/[:space:]]+: ]]
}

diagnose_git_failure() {
  local log="$1"
  if grep -Fq "Host key verification failed" "$log"; then
    echo "✗ Git SSH 主机指纹校验失败：Runner 尚未信任代码仓库服务器。" >&2
    echo "  请在资源中心编辑 Git SSH 凭据，重新获取并确认服务器指纹；平台不会绕过 StrictHostKeyChecking。" >&2
  elif grep -Eqi "Permission denied \(publickey\)|no supported authentication methods" "$log"; then
    echo "✗ Git SSH 身份认证失败：私钥对应的公钥未加入代码托管账号，或该账号没有仓库读取权限。" >&2
    echo "  请核对资源中心选择的 SSH 私钥、Git 用户（通常为 git）和仓库授权。" >&2
  elif grep -Eqi "Authentication failed|HTTP Basic: Access denied|invalid credentials|status code: (401|403)|The requested URL returned error: (401|403)" "$log"; then
    echo "✗ Git HTTPS 身份认证失败：请检查应用是否选择了正确的 Git 代码凭据，以及用户名和 PAT 是否有效、是否具有仓库读取权限。" >&2
  elif grep -Eqi "repository .* not found|Repository not found|does not appear to be a git repository" "$log"; then
    echo "✗ Git 仓库不存在或当前凭据无权访问：请核对仓库地址、组织路径和读取权限。" >&2
  elif grep -Eqi "Could not resolve host|Name or service not known|Connection timed out|Failed to connect" "$log"; then
    echo "✗ Git 服务器网络不可达：请检查 DNS、代理、防火墙和 Runner 到代码仓库的网络。" >&2
  elif grep -Eqi "Unknown option: -C|unknown option.*-C" "$log"; then
    echo "✗ Runner 上的 Git 版本过旧，不支持 -C 参数。请升级 Git，或使用已兼容 Git 1.8 的平台版本。" >&2
  elif is_ssh_url; then
    echo "✗ Git SSH 拉取失败：请检查资源中心的 SSH 私钥、已确认主机指纹和仓库读取权限。" >&2
  else
    echo "✗ Git HTTPS 拉取失败：请检查仓库地址、Git 代码凭据和网络连接。" >&2
  fi
}

prepare_ssh_auth() {
  [ -n "${GIT_SSH_PRIVATE_KEY:-}" ] || return 0
  if [ -z "${GIT_SSH_KNOWN_HOSTS:-}" ]; then
    echo "✗ Git SSH 凭据不完整：缺少已确认的服务器主机指纹。" >&2
    echo "  请在资源中心编辑该 Git 凭据，点击“获取指纹”并确认后再保存。" >&2
    exit 64
  fi

  ssh_material_dir=$(mktemp -d "${TMPDIR:-/tmp}/devopsplatform-git-ssh.XXXXXX")
  umask 077
  printf '%s\n' "$GIT_SSH_PRIVATE_KEY" > "$ssh_material_dir/id_key"
  printf '%s\n' "$GIT_SSH_KNOWN_HOSTS" > "$ssh_material_dir/known_hosts"

  local host_key_type host_key_algorithms
  host_key_type=$(awk 'NF >= 3 && $1 !~ /^#/ { print $2; exit }' "$ssh_material_dir/known_hosts")
  case "$host_key_type" in
    ssh-rsa) host_key_algorithms="rsa-sha2-512,rsa-sha2-256,ssh-rsa" ;;
    ssh-ed25519|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521) host_key_algorithms="$host_key_type" ;;
    *)
      echo "✗ Git SSH 主机指纹配置无效：不支持的主机公钥算法 ${host_key_type:-unknown}。" >&2
      echo "  请在资源中心重新获取并确认 Git 服务器指纹。" >&2
      exit 64
      ;;
  esac

  # Git 1.8 (still common on CentOS 7) does not support GIT_SSH_COMMAND. A
  # temporary GIT_SSH wrapper works on both old and current Git versions and
  # keeps all secret paths/values out of command-line construction.
  cat > "$ssh_material_dir/ssh-wrapper" <<'EOF'
#!/bin/sh
if [ -n "${DEVOPS_GIT_SSH_USER:-}" ]; then
  exec ssh -i "$DEVOPS_GIT_SSH_KEY_FILE" \
    -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o CheckHostIP=no \
    -o "HostKeyAlgorithms=$DEVOPS_GIT_SSH_HOST_KEY_ALGORITHMS" \
    -o "UserKnownHostsFile=$DEVOPS_GIT_SSH_KNOWN_HOSTS_FILE" \
    -l "$DEVOPS_GIT_SSH_USER" "$@"
fi
exec ssh -i "$DEVOPS_GIT_SSH_KEY_FILE" \
  -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o CheckHostIP=no \
  -o "HostKeyAlgorithms=$DEVOPS_GIT_SSH_HOST_KEY_ALGORITHMS" \
  -o "UserKnownHostsFile=$DEVOPS_GIT_SSH_KNOWN_HOSTS_FILE" "$@"
EOF
  chmod 700 "$ssh_material_dir/ssh-wrapper"
  export DEVOPS_GIT_SSH_KEY_FILE="$ssh_material_dir/id_key"
  export DEVOPS_GIT_SSH_KNOWN_HOSTS_FILE="$ssh_material_dir/known_hosts"
  export DEVOPS_GIT_SSH_HOST_KEY_ALGORITHMS="$host_key_algorithms"
  export DEVOPS_GIT_SSH_USER="${GIT_USERNAME:-}"
  export GIT_SSH="$ssh_material_dir/ssh-wrapper"
  unset GIT_SSH_COMMAND

  if [ -n "${GIT_SSH_PASSPHRASE:-}" ]; then
    cat > "$ssh_material_dir/askpass" <<'EOF'
#!/bin/sh
printf '%s\n' "$GIT_SSH_PASSPHRASE"
EOF
    chmod 700 "$ssh_material_dir/askpass"
    export SSH_ASKPASS="$ssh_material_dir/askpass"
    export SSH_ASKPASS_REQUIRE=force
    export DISPLAY="${DISPLAY:-devopsplatform:0}"
  fi
}

run_git() {
  local log status
  log=$(mktemp "${TMPDIR:-/tmp}/devopsplatform-git.XXXXXX")
  set +e
  "$@" 2>&1 | tee "$log"
  status=${PIPESTATUS[0]}
  set -e
  if (( status != 0 )); then
    diagnose_git_failure "$log"
  fi
  rm -f -- "$log"
  return "$status"
}

git_auth() {
  if [ -n "${GIT_PASSWORD:-}" ]; then
    run_git git -c credential.helper='!f() { printf "%s\n" "username=$GIT_USERNAME" "password=$GIT_PASSWORD"; }; f' "$@"
  else
    run_git git "$@"
  fi
}

if is_ssh_url && [ -n "${GIT_PASSWORD:-}" ]; then
  echo "✗ Git 认证方式不匹配：仓库地址使用 SSH，但应用选择的是 HTTPS 用户名/PAT 凭据。" >&2
  echo "  请把仓库地址改为 https://...，或者在资源中心新建 SSH 私钥凭据并为应用选择它。" >&2
  exit 64
fi
if ! is_ssh_url && [ -n "${GIT_SSH_PRIVATE_KEY:-}" ]; then
  echo "✗ Git 认证方式不匹配：仓库地址使用 HTTPS，但应用选择的是 SSH 私钥凭据。" >&2
  echo "  请把仓库地址改为 git@... 或 ssh://...，或者改选 HTTPS/PAT 凭据。" >&2
  exit 64
fi

prepare_ssh_auth

if [ ! -d "$LP_WORKSPACE/.git" ]; then
  echo "⬇ clone $URL"
  if [ -n "$BR" ]; then git_auth clone "$URL" "$LP_WORKSPACE" -b "$BR"
  else git_auth clone "$URL" "$LP_WORKSPACE"; fi
else
  REF="origin/HEAD"; [ -n "$BR" ] && REF="origin/$BR"
  echo "⬇ fetch + reset $REF"
  # CentOS 7 commonly ships Git 1.8, which predates `git -C`. Use the
  # long-standing repository options so incremental checkouts work there too.
  # Refresh origin before fetching: an application may switch between SSH and
  # HTTPS credentials while retaining the same workspace.
  git --git-dir="$LP_WORKSPACE/.git" --work-tree="$LP_WORKSPACE" remote set-url origin "$URL"
  git_auth --git-dir="$LP_WORKSPACE/.git" --work-tree="$LP_WORKSPACE" fetch --all --prune
  git_auth --git-dir="$LP_WORKSPACE/.git" --work-tree="$LP_WORKSPACE" reset --hard "$REF"
  git_auth --git-dir="$LP_WORKSPACE/.git" --work-tree="$LP_WORKSPACE" clean -fd
fi
git --git-dir="$LP_WORKSPACE/.git" --work-tree="$LP_WORKSPACE" rev-parse --short HEAD > "$LP_OUTPUTS_FILE.new" 2>/dev/null || true
echo "COMMIT=$(git --git-dir="$LP_WORKSPACE/.git" --work-tree="$LP_WORKSPACE" rev-parse --short HEAD 2>/dev/null)" >> "$LP_OUTPUTS_FILE"
