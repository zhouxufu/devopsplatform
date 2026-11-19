#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
plugin_os="${GOOS:-$(go env GOOS)}"
plugin_arch="${GOARCH:-$(go env GOARCH)}"
plugin_output="${1:-dist-plugins/release-rules-${plugin_os}-${plugin_arch}.zip}"
plugin_stage="$(mktemp -d)"
trap 'rm -rf "$plugin_stage"' EXIT
mkdir -p "$plugin_stage/bin" "$(dirname "$plugin_output")"
if [ "$plugin_os" = darwin ] && [ "$(go env GOOS)" = darwin ]; then
 CGO_ENABLED=1 go build -trimpath -ldflags='-s -w -linkmode=external' -o "$plugin_stage/bin/rules-plugin" ./cmd/rules-plugin
else
 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$plugin_stage/bin/rules-plugin" ./cmd/rules-plugin
fi
python3 - "$plugin_stage" "$plugin_output" "$plugin_os" "$plugin_arch" <<'PY'
import hashlib,json,pathlib,sys,zipfile
stage,out,system,arch=sys.argv[1:];stage=pathlib.Path(stage)
manifest={'id':'release-rules','name':'通用上线规则编译器','version':'1.0.0','protocol':1,'os':system,'arch':arch,'entrypoint':'bin/rules-plugin','sha256':hashlib.sha256((stage/'bin/rules-plugin').read_bytes()).hexdigest(),'capabilities':['policy.release.v1'],'access':{'policy.release.v1':'admin'},'configuration':[]}
(stage/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
(stage/'README.txt').write_text('独立规则编译插件，无需凭据或 JSON 配置。安装后启用，在研发工作台→设置→上线识别规则选择本插件。\n编译产物是有界声明式规则，由平台验证并执行，不执行脚本、SQL 或远程写入。人工覆盖优先，缺字段待确认。\n停用插件阻止新的编译/预览/保存，已有规则版本继续由平台执行，不静默改变已保存策略。\n')
with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
 for f in stage.rglob('*'):
  if f.is_file():z.write(f,f.relative_to(stage).as_posix())
print(out)
PY
