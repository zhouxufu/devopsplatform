#!/bin/bash
set -e
target=$(compgen -G "$LP_WORKSPACE/$LP_GLOB" | tail -1)
[ -z "$target" ] && { echo "✗ 未匹配到 $LP_GLOB"; exit 1; }
[ -d "$target" ] || { echo "✗ 静态制品必须是目录: $target"; exit 1; }
dst="$LP_OUTPUT_DIR/$LP_SERVICE-$LP_VERSION.tar.gz"
tar -czf "$dst" -C "$(dirname "$target")" "$(basename "$target")"
echo "ARTIFACT=$dst" >> "$LP_OUTPUTS_FILE"
echo "ARTIFACT_KIND=static-archive" >> "$LP_OUTPUTS_FILE"
echo "✓ 制品: $dst"
