#!/bin/bash
# 本地开发一键启动（生产环境使用 devopsplatformctl）
set -e
cd "$(dirname "$0")/.."
# 读取本地保存的登录配置；显式提供的登录配置优先。
if [[ -z "${AUTH:-${DEVOPSPLATFORM_AUTH:-${LOGPLATFORM_AUTH:-}}}" && -f runtime/local-login.env ]]; then
  source runtime/local-login.env
fi
# The log plugin has one backend. Start the local dependency only when no
# explicit external endpoint is configured; never fall back to SQLite logs.
if [[ -z "${DEVOPSPLATFORM_VICTORIALOGS_URL:-}" ]]; then
  if ! curl -fsS --max-time 2 http://127.0.0.1:9428/health >/dev/null 2>&1; then
    [[ -x bin/victoria-logs-prod ]] || { echo '先运行 python3 scripts/fetch-victorialogs.py 安装日志存储依赖' >&2; exit 1; }
    mkdir -p runtime/victorialogs
    GOMAXPROCS=2 GOMEMLIMIT=1GiB nohup ./bin/victoria-logs-prod -httpListenAddr=127.0.0.1:9428 -storageDataPath=runtime/victorialogs -retentionPeriod=7d -retention.maxDiskSpaceUsageBytes=20GiB -inmemoryDataFlushInterval=1s -insert.maxLineSizeBytes=2MiB -memory.allowedBytes=256MiB > runtime/victorialogs.log 2>&1 &
    ready=0
    for i in {1..40}; do
      if curl -fsS --max-time 1 http://127.0.0.1:9428/health >/dev/null 2>&1; then ready=1; break; fi
      sleep .25
    done
    [[ "$ready" == 1 ]] || { echo 'VictoriaLogs 启动失败，请检查 runtime/victorialogs.log' >&2; exit 1; }
  fi
fi
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
