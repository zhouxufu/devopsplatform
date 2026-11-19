#!/bin/bash
set -euo pipefail
cd "$LP_WORKSPACE"

image="${LP_IMAGE_NAME:?IMAGE_NAME 不能为空}"
context="${LP_CONTEXT:-.}"
dockerfile="${LP_DOCKERFILE:-Dockerfile}"
source="${LP_DOCKERFILE_SOURCE:-repository}"
case "$source" in
  repository) ;;
  platform|template)
    [ -n "${LP_DOCKERFILE_CONTENT:-}" ] || { echo "✗ 平台托管 Dockerfile 内容不能为空"; exit 1; }
    dockerfile="-"
    ;;
  *) echo "✗ Dockerfile 来源无效"; exit 1;;
esac
tag="$image:$LP_VERSION"
# A Runner needs Buildx; a deployment target only needs ordinary Engine/CLI.
# Detect this before logging in, building, or pushing anything.
command -v docker >/dev/null || { echo "✗ 构建 Runner 未安装 Docker CLI"; exit 1; }
docker version --format 'Docker Client {{.Client.Version}} / Server {{.Server.Version}}' || { echo "✗ 构建 Runner 无法访问 Docker 守护进程，请检查权限及连接"; exit 1; }
docker buildx version >/dev/null 2>&1 || { echo "✗ 构建 Runner 缺少 Docker Buildx 插件，请安装 docker-buildx-plugin；部署目标机不要求安装 Buildx"; exit 1; }
docker buildx inspect >/dev/null 2>&1 || { echo "✗ 构建 Runner 的 Buildx builder 不可用，请检查构建器和 Docker 连接"; exit 1; }
args=(docker buildx build --file "$dockerfile" --tag "$tag")
if [ -n "${LP_PLATFORMS:-}" ]; then args+=(--platform "$LP_PLATFORMS"); fi

build_image() {
  if [ "$source" = platform ] || [ "$source" = template ]; then
    # Dockerfile on stdin; keep the project context and its .dockerignore intact.
    # Never overwrite/add files to the checked out source or print managed text.
    printf '%s' "$LP_DOCKERFILE_CONTENT" | "${args[@]}"
  else
    "${args[@]}"
  fi
}

if [ "${LP_PUSH:-true}" = "true" ]; then
  if [ -n "${DOCKER_REGISTRY:-}" ] && [ -n "${DOCKER_USERNAME:-}" ] && [ -n "${DOCKER_PASSWORD:-}" ]; then
    printf '%s' "$DOCKER_PASSWORD" | docker login "$DOCKER_REGISTRY" --username "$DOCKER_USERNAME" --password-stdin >/dev/null
  fi
  args+=(--push "$context")
  build_image
  digest=$(docker buildx imagetools inspect "$tag" | awk '/^Digest:/ {print $2; exit}')
  [ -n "$digest" ] || { echo "✗ 无法读取镜像 digest"; exit 1; }
  uri="$image@$digest"
else
  # Offline delivery has one concrete platform, never a multi-platform index.
  case "${LP_PLATFORMS:-}" in *,*) echo "✗ 镜像文件交付只支持一个目标平台"; exit 1;; esac
  args+=(--load "$context")
  build_image
  mkdir -p "$LP_OUTPUT_DIR"
  uri="$LP_OUTPUT_DIR/docker-image.tar"
  docker image save --output "$uri" "$tag"
  # The server hashes the archive and derives image identity/platform from it.
  echo "ARTIFACT=$uri" >> "$LP_OUTPUTS_FILE"
  echo "ARTIFACT_KIND=docker-image-archive" >> "$LP_OUTPUTS_FILE"
  echo "✓ 镜像文件制品: $uri"
  exit 0
fi

echo "ARTIFACT=$uri" >> "$LP_OUTPUTS_FILE"
echo "ARTIFACT_KIND=oci-image" >> "$LP_OUTPUTS_FILE"
echo "ARTIFACT_DIGEST=$digest" >> "$LP_OUTPUTS_FILE"
echo "✓ OCI 制品: $uri"
