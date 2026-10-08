#!/bin/bash
# 本地开发一键启动（生产环境使用 devopsplatformctl）
set -e
cd "$(dirname "$0")/.."
./scripts/stop-all.sh 2>/dev/null || true
# 本地 agent.yaml 默认使用 demo-token；生产包会自动生成独立数据面 Token。
DEVOPSPLATFORM_AUTH="${AUTH:-${DEVOPSPLATFORM_AUTH:-${LOGPLATFORM_AUTH:-}}}" \
DEVOPSPLATFORM_DATA_TOKEN="${DEVOPSPLATFORM_DATA_TOKEN:-demo-token}" \
nohup ./bin/platform -addr 127.0.0.1:9800 -plugins ./plugins -data ./runtime/platform-data > runtime/platform.log 2>&1 &
sleep 3
nohup ./bin/agent -config agent.yaml > runtime/agent.log 2>&1 &
sleep 1
echo "✅ platform  http://127.0.0.1:9800   (logs→:9801 admin→127.0.0.1:19801, monitor→:9802)"
tail -2 runtime/agent.log
