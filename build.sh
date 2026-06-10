#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="sbgw"
DIST_DIR="${ROOT_DIR}/dist"
IMAGE_JSON="${ROOT_DIR}/images/image.json"
INSTALL_SH="${ROOT_DIR}/install.sh"
VERSION_FILE="${ROOT_DIR}/VERSION"

ARCH="amd64"
VERSION="${VERSION:-}"
COMMIT="${COMMIT:-}"
DATE="${DATE:-}"
PUSH_AFTER_BUILD="false"

usage() {
  cat <<USAGE
Usage:
  bash build.sh --arch amd64|arm64|all [--version <version>] [--push]

Build offline .run delivery packages for sbgw.

Examples:
  bash build.sh --arch amd64
  bash build.sh --arch arm64
  bash build.sh --arch all --version v0.1.0
USAGE
}

log() { printf '[INFO] %s\n' "$*"; }
ok() { printf '[OK] %s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch) ARCH="${2:-}"; shift 2 ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --push) PUSH_AFTER_BUILD="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "Unknown argument: $1" ;;
  esac
done

[[ -f "${INSTALL_SH}" ]] || die "install.sh not found"
[[ -f "${IMAGE_JSON}" ]] || die "images/image.json not found"
[[ -f "${ROOT_DIR}/Dockerfile" ]] || die "Dockerfile not found"
grep -qx '__PAYLOAD_BELOW__' "${INSTALL_SH}" || die "install.sh must contain a standalone __PAYLOAD_BELOW__ marker line"

for cmd in docker tar sha256sum python3 awk sed; do
  command -v "$cmd" >/dev/null 2>&1 || die "Required command not found: $cmd"
done

if [[ -z "${VERSION}" ]]; then
  if [[ -f "${VERSION_FILE}" ]]; then
    VERSION="$(tr -d '[:space:]' < "${VERSION_FILE}")"
  else
    VERSION="dev"
  fi
fi
[[ -n "${COMMIT}" ]] || COMMIT="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo none)"
[[ -n "${DATE}" ]] || DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

case "${ARCH}" in
  amd64) ARCHES=(amd64) ;;
  arm64) ARCHES=(arm64) ;;
  all) ARCHES=(amd64 arm64) ;;
  *) die "--arch must be amd64, arm64 or all" ;;
esac

mkdir -p "${DIST_DIR}"

json_entries_for_arch() {
  local arch="$1"
  python3 - "${IMAGE_JSON}" "${arch}" "${VERSION}" <<'PY'
import json, sys
path, want_arch, version = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, 'r', encoding='utf-8') as f:
    data = json.load(f)
for item in data:
    if item.get('arch') != want_arch:
        continue
    fields = [
        item.get('name', ''),
        item.get('arch', ''),
        item.get('platform', ''),
        item.get('pull', ''),
        item.get('dockerfile', ''),
        item.get('context', '.'),
        item.get('tag', '').replace('{{VERSION}}', version),
        item.get('tar', ''),
    ]
    print('|'.join(fields))
PY
}

copy_payload_common() {
  local payload_dir="$1"
  mkdir -p "${payload_dir}/manifests" "${payload_dir}/config"
  cp -a "${ROOT_DIR}/manifests/." "${payload_dir}/manifests/"
  cp "${ROOT_DIR}/config.example.yaml" "${payload_dir}/config/config.example.yaml"
  cp "${ROOT_DIR}/VERSION" "${payload_dir}/VERSION" 2>/dev/null || printf '%s\n' "${VERSION}" > "${payload_dir}/VERSION"
}

build_one_arch() {
  local arch="$1"
  local build_dir="${ROOT_DIR}/.build-payload-${arch}"
  local payload_dir="${build_dir}/payload"
  local payload_tgz="${build_dir}/payload.tar.gz"
  local run_file="${DIST_DIR}/${APP_NAME}-${VERSION}-linux-${arch}.run"

  rm -rf "${build_dir}"
  mkdir -p "${payload_dir}/images"
  copy_payload_common "${payload_dir}"

  local entries
  entries="$(json_entries_for_arch "${arch}")"
  [[ -n "${entries}" ]] || die "No image entries found for arch=${arch} in images/image.json"

  printf 'name|tar_name|load_ref|default_target_ref|platform|pull|dockerfile\n' > "${payload_dir}/images/image-index.tsv"
  printf '%s\n' "${entries}" | while IFS='|' read -r name item_arch platform pull dockerfile context tag tar_name; do
    [[ -n "${name}" ]] || die "image.name is required for arch=${arch}"
    [[ -n "${platform}" ]] || die "image.platform is required for ${name}/${arch}"
    [[ -n "${tag}" ]] || die "image.tag is required for ${name}/${arch}"
    [[ -n "${tar_name}" ]] || die "image.tar is required for ${name}/${arch}"

    log "Preparing image ${name} arch=${arch} platform=${platform} tag=${tag}"
    if [[ -n "${dockerfile}" ]]; then
      docker buildx build \
        --platform "${platform}" \
        --load \
        --build-arg TARGETOS="linux" \
        --build-arg TARGETARCH="${arch}" \
        --build-arg VERSION="${VERSION}" \
        --build-arg COMMIT="${COMMIT}" \
        --build-arg DATE="${DATE}" \
        -f "${ROOT_DIR}/${dockerfile}" \
        -t "${tag}" \
        "${ROOT_DIR}/${context}"
    elif [[ -n "${pull}" ]]; then
      docker pull --platform "${platform}" "${pull}"
      docker tag "${pull}" "${tag}"
    else
      die "image ${name}/${arch} must set either dockerfile or pull"
    fi

    docker save "${tag}" -o "${payload_dir}/images/${tar_name}"
    printf '%s|%s|%s|%s|%s|%s|%s\n' "${name}" "${tar_name}" "${tag}" "${tag}" "${platform}" "${pull}" "${dockerfile}" >> "${payload_dir}/images/image-index.tsv"
  done

  cp "${IMAGE_JSON}" "${payload_dir}/images/image.json"
  (cd "${payload_dir}" && tar -czf "${payload_tgz}" .)
  tar -tzf "${payload_tgz}" >/dev/null

  cat "${INSTALL_SH}" "${payload_tgz}" > "${run_file}"
  chmod +x "${run_file}"
  sha256sum "${run_file}" > "${run_file}.sha256"
  ok "Built ${run_file}"
}

for a in "${ARCHES[@]}"; do
  build_one_arch "$a"
done

ok "Artifacts are under ${DIST_DIR}"
