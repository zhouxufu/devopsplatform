#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C LANG=C

repo_dir=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

arch=${1:-amd64}
version=${2:-$(date -u +%Y%m%d%H%M%S)}
case "$arch" in
  amd64|arm64) ;;
  x86_64) arch=amd64 ;;
  aarch64) arch=arm64 ;;
  *) echo "不支持的架构: ${arch}（仅支持 amd64/arm64）" >&2; exit 2 ;;
esac
case "$version" in
  *[!0-9A-Za-z._-]*|'') echo "版本只能包含字母、数字、点、下划线和横线" >&2; exit 2 ;;
esac

go_bin=${GO_BIN:-go}
if ! command -v "$go_bin" >/dev/null 2>&1; then
  echo "找不到 Go；请使用 GO_BIN=/绝对路径/go $0 $arch $version" >&2
  exit 2
fi

if [ "${SKIP_WEB_BUILD:-0}" != "1" ]; then
  ./scripts/build-web.sh
fi

package_name="devopsplatform-${version}-linux-${arch}"
staging_dir="$repo_dir/dist/.release-${package_name}-$$"
package_dir="$staging_dir/$package_name"
archive="$repo_dir/dist/${package_name}.tar.gz"
cleanup() {
  case "$staging_dir" in
    "$repo_dir"/dist/.release-*) rm -rf "$staging_dir" ;;
  esac
}
trap cleanup EXIT

mkdir -p "$package_dir/bin" "$package_dir/dist"

build() {
  local output=$1 package=$2
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$go_bin" build \
    -trimpath -ldflags="-s -w" -o "$package_dir/bin/$output" "$package"
}

build devopsplatform ./cmd/platform
build logserver ./cmd/server
mkdir -p "$package_dir/support"
python3 scripts/fetch-victorialogs.py --os linux --arch "$arch" --output "$package_dir/bin/victoria-logs-prod" --version-output "$package_dir/support/VICTORIALOGS-VERSION"
cp deploy/victorialogs.service deploy/VICTORIALOGS.md deploy/licenses/VictoriaLogs-LICENSE.txt "$package_dir/support/"
build monitor ./cmd/monitor
build deploy ./cmd/deploy
build devtools ./cmd/devtools
build migrate-registry ./cmd/migrate-registry
build migrate-deploy ./cmd/migrate-deploy
build migrate-monitor ./cmd/migrate-monitor

GO_BIN="$go_bin" AGENT_VERSION="$version" ./scripts/build-agents.sh
cp -R plugins steps "$package_dir/"
cp -R dist/agents "$package_dir/dist/"
cp deploy/devopsplatformctl "$package_dir/devopsplatformctl"
chmod +x "$package_dir/devopsplatformctl" "$package_dir/bin/"*
printf '%s\n' "$version" > "$package_dir/VERSION"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$package_dir" && find bin dist/agents support VERSION devopsplatformctl -type f -print | LC_ALL=C sort | xargs sha256sum > SHA256SUMS)
else
  (cd "$package_dir" && find bin dist/agents support VERSION devopsplatformctl -type f -print | LC_ALL=C sort | xargs shasum -a 256 > SHA256SUMS)
fi

mkdir -p "$repo_dir/dist"
if command -v xattr >/dev/null 2>&1; then
  xattr -cr "$package_dir" || true
fi
COPYFILE_DISABLE=1 tar --no-xattrs -C "$staging_dir" -czf "${archive}.tmp" "$package_name"
mv "${archive}.tmp" "$archive"
cp deploy/devopsplatformctl "$repo_dir/dist/devopsplatformctl"
chmod +x "$repo_dir/dist/devopsplatformctl"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$repo_dir/dist" && sha256sum "$(basename "$archive")" > "$(basename "$archive").sha256")
else
  (cd "$repo_dir/dist" && shasum -a 256 "$(basename "$archive")" > "$(basename "$archive").sha256")
fi

echo "✅ Linux 发布包已生成"
echo "   $archive"
echo "   ${archive}.sha256"
echo "   $repo_dir/dist/devopsplatformctl"
