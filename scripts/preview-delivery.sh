#!/usr/bin/env bash
# Standalone, isolated preview. Never stop or reuse the regular platform data.
set -Eeuo pipefail
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=$(cd "${SCRIPT_DIR}/.." && pwd)
cd "${PROJECT_ROOT}"
GO_BIN=${GO_BIN:-go}
PREVIEW_ADDR=${PREVIEW_ADDR:-127.0.0.1:19813}
PREVIEW_DATA=${PREVIEW_DATA:-${PROJECT_ROOT}/runtime/delivery-preview}
if [[ ! ${PREVIEW_ADDR} =~ ^127\.0\.0\.1:[0-9]+$ ]]; then
  echo 'Preview must listen on 127.0.0.1 only; use the platform gateway for shared access.' >&2
  exit 2
fi
command -v "${GO_BIN}" >/dev/null || { echo "Go is required: set GO_BIN=/absolute/path/to/go" >&2; exit 2; }
mkdir -p "${PROJECT_ROOT}/bin"
"${GO_BIN}" build -o "${PROJECT_ROOT}/bin/delivery-preview" ./cmd/deploy
echo "Isolated delivery preview: http://${PREVIEW_ADDR}/ui"
echo "Data: ${PREVIEW_DATA}; Ctrl+C stops this preview only."
exec "${PROJECT_ROOT}/bin/delivery-preview" -admin-addr "${PREVIEW_ADDR}" -data "${PREVIEW_DATA}" -bus http://127.0.0.1:1
