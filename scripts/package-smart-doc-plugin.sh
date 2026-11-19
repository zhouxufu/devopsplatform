#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
plugin_go="${GO_BIN:-go}"
plugin_os="${GOOS:-$("$plugin_go" env GOOS)}"
plugin_arch="${GOARCH:-$("$plugin_go" env GOARCH)}"
plugin_output="${1:-dist-plugins/smart-doc-${plugin_os}-${plugin_arch}.zip}"
plugin_stage="$(mktemp -d)"
trap 'rm -rf "$plugin_stage"' EXIT
mkdir -p "$plugin_stage/bin" "$(dirname "$plugin_output")"
if [ "$plugin_os" = darwin ] && [ "$("$plugin_go" env GOOS)" = darwin ]; then
 CGO_ENABLED=1 "$plugin_go" build -trimpath -ldflags='-s -w -linkmode=external' -o "$plugin_stage/bin/smart-doc-plugin" ./cmd/smart-doc-plugin
else
 CGO_ENABLED=0 "$plugin_go" build -trimpath -ldflags='-s -w' -o "$plugin_stage/bin/smart-doc-plugin" ./cmd/smart-doc-plugin
fi
python3 scripts/package-smart-doc.py "$plugin_stage" "$plugin_output" "$plugin_os" "$plugin_arch"
