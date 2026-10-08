#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

GO_BIN="${GO_BIN:-go}"
"$GO_BIN" version >/dev/null
./scripts/build-web.sh

mkdir -p bin
build() {
  local output="$1" package="$2"
  CGO_ENABLED=0 "$GO_BIN" build -ldflags="-s -w" -trimpath -o "$output" "$package"
}
build bin/platform ./cmd/platform
build bin/logserver ./cmd/server
build bin/server ./cmd/server
build bin/monitor ./cmd/monitor
build bin/deploy ./cmd/deploy
build bin/devtools ./cmd/devtools
build bin/migrate-registry ./cmd/migrate-registry
build bin/migrate-deploy ./cmd/migrate-deploy
build bin/migrate-monitor ./cmd/migrate-monitor
build bin/agent ./cmd/agent
GO_BIN="$GO_BIN" ./scripts/build-agents.sh

echo "✅ platform / logserver(server) / monitor / deploy / devtools / migrate-registry / migrate-deploy / migrate-monitor / agent 构建完成"
