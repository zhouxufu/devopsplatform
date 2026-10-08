#!/bin/bash
set -euo pipefail
cd "$LP_WORKSPACE"

image="${LP_IMAGE_NAME:?IMAGE_NAME 不能为空}"
context="${LP_CONTEXT:-.}"
dockerfile="${LP_DOCKERFILE:-Dockerfile}"
tag="$image:$LP_VERSION"
args=(docker buildx build --file "$dockerfile" --tag "$tag")
if [ -n "${LP_PLATFORMS:-}" ]; then args+=(--platform "$LP_PLATFORMS"); fi

if [ "${LP_PUSH:-true}" = "true" ]; then
  if [ -n "${DOCKER_REGISTRY:-}" ] && [ -n "${DOCKER_USERNAME:-}" ] && [ -n "${DOCKER_PASSWORD:-}" ]; then
    printf '%s' "$DOCKER_PASSWORD" | docker login "$DOCKER_REGISTRY" --username "$DOCKER_USERNAME" --password-stdin >/dev/null
  fi
  args+=(--push "$context")
  "${args[@]}"
  digest=$(docker buildx imagetools inspect "$tag" | awk '/^Digest:/ {print $2; exit}')
  [ -n "$digest" ] || { echo "✗ 无法读取镜像 digest"; exit 1; }
  uri="$image@$digest"
else
  args+=(--load "$context")
  "${args[@]}"
  digest=$(docker image inspect "$tag" --format '{{.Id}}')
  uri="$tag"
fi

echo "ARTIFACT=$uri" >> "$LP_OUTPUTS_FILE"
echo "ARTIFACT_KIND=oci-image" >> "$LP_OUTPUTS_FILE"
echo "ARTIFACT_DIGEST=$digest" >> "$LP_OUTPUTS_FILE"
echo "✓ OCI 制品: $uri"
