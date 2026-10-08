#!/bin/bash
# 停止整套 LogPlatform（platform 退出会带走插件进程）
cd "$(dirname "$0")/.."
pkill -f 'bin/agent' 2>/dev/null
pkill -f 'bin/monitor' 2>/dev/null
pkill -f 'bin/logserver' 2>/dev/null
pkill -f 'bin/platform' 2>/dev/null
sleep 1
pgrep -fl 'bin/(platform|agent|logserver|monitor)' || echo "✅ 全部已停止"
