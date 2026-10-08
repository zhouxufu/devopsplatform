#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

GO_BIN="${GO_BIN:-go}"
AGENT_VERSION="${AGENT_VERSION:-dev}"
mkdir -p dist/agents

for arch in amd64 arm64; do
  output="dist/agents/logagent-linux-${arch}"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO_BIN" build \
    -ldflags="-s -w -X main.version=${AGENT_VERSION}" -trimpath -o "$output" ./cmd/agent
done

if command -v sha256sum >/dev/null 2>&1; then
  (cd dist/agents && sha256sum logagent-linux-amd64 logagent-linux-arm64 > SHA256SUMS)
else
  (cd dist/agents && shasum -a 256 logagent-linux-amd64 logagent-linux-arm64 > SHA256SUMS)
fi

echo "✅ Linux Agent 安装包：amd64 / arm64"
