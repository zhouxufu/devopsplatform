#!/bin/bash
# 前端构建：vite → 拷贝到各插件 embed 目录 → Go 编译
set -e
cd "$(dirname "$0")/.."
cd web
npm run build
cd ..
# 三个插件共享多入口产物，各自嵌入同一份静态资源；平台壳只需要 shell 入口。
for target in internal/deploy/assets/app internal/monitor/assets/app internal/server/assets/app internal/devtools/assets/app; do
  rm -rf "$target"
  mkdir -p "$target"
  cp -R web/dist/. "$target/"
done
rm -rf internal/platform/assets/assets
mkdir -p internal/platform/assets/assets
cp -r web/dist/assets/* internal/platform/assets/assets/
cp web/dist/shell.html internal/platform/assets/shell.html
cp web/dist/theme-notices.txt internal/platform/assets/theme-notices.txt
echo "✅ 前端产物已同步到 platform/logs/monitor/deploy embed 目录"
