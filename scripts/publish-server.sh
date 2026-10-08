#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "用法: $0 用户@服务器 发布包.tar.gz" >&2
  exit 2
fi

server=$1
archive=$2
script_dir=$(cd "$(dirname "$0")" && pwd)
repo_dir=$(cd "$script_dir/.." && pwd)
ctl="$repo_dir/deploy/devopsplatformctl"

[ -f "$archive" ] || { echo "发布包不存在: $archive" >&2; exit 1; }
[ -f "${archive}.sha256" ] || { echo "校验文件不存在: ${archive}.sha256" >&2; exit 1; }

remote_archive="/tmp/$(basename "$archive")"
scp "$archive" "${archive}.sha256" "$ctl" "$server:/tmp/"
ssh -t "$server" "chmod +x /tmp/devopsplatformctl && sudo /tmp/devopsplatformctl apply '$remote_archive'"

