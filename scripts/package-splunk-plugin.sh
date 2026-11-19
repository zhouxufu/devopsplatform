#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
# Build the adapter separately. Nothing is installed/enabled by packaging.
plugin_os="${GOOS:-$(go env GOOS)}"
plugin_arch="${GOARCH:-$(go env GOARCH)}"
plugin_output="${1:-dist-plugins/splunk-logs-${plugin_os}-${plugin_arch}.zip}"
plugin_stage="$(mktemp -d)"
trap 'rm -rf "$plugin_stage"' EXIT
mkdir -p "$plugin_stage/bin" "$(dirname "$plugin_output")"
if [ "$plugin_os" = darwin ] && [ "$(go env GOOS)" = darwin ]; then
  CGO_ENABLED=1 GOOS="$plugin_os" GOARCH="$plugin_arch" go build -trimpath -ldflags='-s -w -linkmode=external' -o "$plugin_stage/bin/splunk-plugin" ./cmd/splunk-plugin
else
  CGO_ENABLED=0 GOOS="$plugin_os" GOARCH="$plugin_arch" go build -trimpath -ldflags='-s -w' -o "$plugin_stage/bin/splunk-plugin" ./cmd/splunk-plugin
fi
python3 - "$plugin_stage" "$plugin_output" "$plugin_os" "$plugin_arch" <<'PY'
import hashlib,json,pathlib,sys,zipfile
stage,out,system,arch=sys.argv[1:];stage=pathlib.Path(stage)
manifest={'id':'splunk-logs','name':'Splunk 日志数据源','version':'1.0.0','protocol':1,'os':system,'arch':arch,'entrypoint':'bin/splunk-plugin','sha256':hashlib.sha256((stage/'bin/splunk-plugin').read_bytes()).hexdigest(),'capabilities':['logs.search.v1'],'access':{'logs.search.v1':'member'},'configuration':[
 {'key':'endpoint','label':'REST API 根地址（通常 https://主机:8089）','type':'string','required':True},
 {'key':'username','label':'Splunk 只读账号','type':'string','required':True},
 {'key':'password','label':'密码','type':'secret','required':True},
 {'key':'users','label':'允许查询的平台注册账号（JSON 字符串数组）','type':'json','required':True},
 {'key':'bindings','label':'应用环境绑定（JSON 数组：service / environment / index / filters）','type':'json','required':True},
 {'key':'fields','label':'字段映射（JSON 对象：level / trace_id / host / path / message）','type':'json'},
 {'key':'tls-insecure','label':'跳过 TLS 证书验证（默认 false；仅自行确认的自签证书）','type':'string'}]}
(stage/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
(stage/'README.txt').write_text('安装不自动连接 Splunk。配置并启用后，可先测试 REST API 连接。网页 search 地址不能作为 API 地址。\n绑定示例：[{"service":"order-api","environment":"prod","index":"main","sourcetype":"app","filters":{"service":"order-api","env":"prod"}}]\n字段示例：{"level":"level","trace_id":"traceId"}\n允许用户示例：["admin"]；无认证本地模式账号为 local。\n历史查询只支持字面量关键词，不支持任意 SPL、Live 或告警。\n')
with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
 for f in stage.rglob('*'):
  if f.is_file(): z.write(f,f.relative_to(stage).as_posix())
print(out)
PY
