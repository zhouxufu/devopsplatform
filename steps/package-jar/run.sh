#!/bin/bash
set -e
selected=""
while IFS= read -r candidate; do
  case "$(basename "$candidate")" in *-sources.*|*-javadoc.*|*-plain.*) continue;; esac
  if [ -z "$selected" ] || [ "$candidate" -nt "$selected" ]; then selected="$candidate"; fi
done < <(compgen -G "$LP_WORKSPACE/$LP_GLOB")
[ -z "$selected" ] && { echo "✗ 未匹配到可部署制品 $LP_GLOB"; exit 1; }
ext="${selected##*.}"
dst="$LP_OUTPUT_DIR/$LP_SERVICE-$LP_VERSION.$ext"
cp "$selected" "$dst"
echo "ARTIFACT=$dst" >> "$LP_OUTPUTS_FILE"
echo "ARTIFACT_KIND=jar" >> "$LP_OUTPUTS_FILE"
echo "✓ 制品: $dst"
