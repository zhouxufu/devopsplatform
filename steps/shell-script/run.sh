#!/usr/bin/env bash
set -Eeuo pipefail

if [[ -z ${LP_WORKSPACE:-} || ! -d ${LP_WORKSPACE} ]]; then
  echo '✗ 脚本节点的源码工作区不存在，请先配置代码来源。' >&2
  exit 64
fi
script=${LP_SCRIPT:-}
if [[ -z ${script//[[:space:]]/} ]]; then
  echo '✗ 脚本内容不能为空。' >&2
  exit 64
fi
cd -- "$LP_WORKSPACE"
# Script text is an intentional build program, not interpolated command syntax.
# Output uses the shared redaction/cancellation pipeline. No automatic release.
bash --noprofile --norc -Eeuo pipefail -c "$script"
