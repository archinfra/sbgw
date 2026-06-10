#!/usr/bin/env bash
set -euo pipefail

APP_NAME="sbgw"
ACTION="${1:-help}"
if [[ $# -gt 0 ]]; then shift; fi

WORKDIR="${TMPDIR:-/tmp}/${APP_NAME}-installer-$$"
PAYLOAD_DIR="${WORKDIR}/payload"
IMAGE_INDEX="${PAYLOAD_DIR}/images/image-index.tsv"
MANIFEST_TEMPLATE="${PAYLOAD_DIR}/manifests/sbgw.yaml.tmpl"
RENDERED_MANIFEST="${WORKDIR}/sbgw.yaml"

NAMESPACE="sbgw"
INSTALL_NAME="sbgw"
REGISTRY="sealos.hub:5000/kube4"
REGISTRY_USER=""
REGISTRY_PASS=""
SKIP_IMAGE_PREPARE="false"
YES="false"
APPLY="true"
DRY_RUN="false"
REPLICAS="1"
SERVICE_TYPE="ClusterIP"
NODE_PORT=""
UPSTREAM_BASE_URL="http://vllm:8000"
AUTH_ENABLED="true"
AUTH_TOKEN="sk-local-dev-001"
WAIT_TIMEOUT="120s"
DELETE_NAMESPACE="false"

log() { printf '[INFO] %s\n' "$*"; }
ok() { printf '[OK] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<USAGE
sbgw offline installer

Usage:
  ./sbgw-<version>-linux-<arch>.run install [options]
  ./sbgw-<version>-linux-<arch>.run status [options]
  ./sbgw-<version>-linux-<arch>.run uninstall [options]
  ./sbgw-<version>-linux-<arch>.run help

Actions:
  install      Load/tag/push image, render Kubernetes manifests and apply them.
  status       Show Kubernetes resources.
  uninstall    Delete Deployment/Service/ConfigMap. Namespace is kept by default.
  help         Show this help.

Install options:
  --registry <repo-prefix>       Target registry namespace, e.g. sealos.hub:5000/kube4
  --registry-user <user>         Target registry username
  --registry-pass <pass>         Target registry password
  --skip-image-prepare           Skip docker load/tag/push
  -n, --namespace <ns>           Kubernetes namespace. Default: sbgw
  --name <name>                  Kubernetes app name. Default: sbgw
  --replicas <n>                 Deployment replicas. Default: 1
  --service-type <type>          ClusterIP|NodePort|LoadBalancer. Default: ClusterIP
  --node-port <port>             NodePort when service-type=NodePort
  --upstream-base-url <url>      Upstream OpenAI-compatible base URL
  --auth-enabled true|false      Enable gateway SK auth. Default: true
  --auth-token <sk>              Gateway SK token. Default: sk-local-dev-001
  --wait-timeout <duration>      kubectl rollout wait timeout. Default: 120s
  --no-apply                     Render manifest only, do not kubectl apply
  --dry-run                      Print rendered manifest, do not apply
  --delete-namespace             uninstall also deletes namespace
  -y, --yes                      Skip confirmation

Example:
  ./sbgw-v0.1.0-linux-amd64.run install \
    --registry sealos.hub:5000/kube4 \
    --registry-user admin \
    --registry-pass passw0rd \
    --upstream-base-url http://qwen-vllm.aict.svc:8000 \
    --auth-token sk-prod-xxx \
    -n aict -y
USAGE
}

parse_bool() {
  case "${1:-}" in
    true|TRUE|True|1|yes|YES|y|Y) printf 'true' ;;
    false|FALSE|False|0|no|NO|n|N) printf 'false' ;;
    *) die "Invalid boolean value: $1" ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --registry) REGISTRY="${2:-}"; shift 2 ;;
    --registry-user) REGISTRY_USER="${2:-}"; shift 2 ;;
    --registry-pass) REGISTRY_PASS="${2:-}"; shift 2 ;;
    --skip-image-prepare) SKIP_IMAGE_PREPARE="true"; shift ;;
    -n|--namespace) NAMESPACE="${2:-}"; shift 2 ;;
    --name) INSTALL_NAME="${2:-}"; shift 2 ;;
    --replicas) REPLICAS="${2:-}"; shift 2 ;;
    --service-type) SERVICE_TYPE="${2:-}"; shift 2 ;;
    --node-port) NODE_PORT="${2:-}"; shift 2 ;;
    --upstream-base-url) UPSTREAM_BASE_URL="${2:-}"; shift 2 ;;
    --auth-enabled) AUTH_ENABLED="$(parse_bool "${2:-}")"; shift 2 ;;
    --auth-token) AUTH_TOKEN="${2:-}"; shift 2 ;;
    --wait-timeout) WAIT_TIMEOUT="${2:-}"; shift 2 ;;
    --no-apply) APPLY="false"; shift ;;
    --dry-run) DRY_RUN="true"; APPLY="false"; shift ;;
    --delete-namespace) DELETE_NAMESPACE="true"; shift ;;
    -y|--yes) YES="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "Unknown argument: $1" ;;
  esac
done

cleanup() { rm -rf "${WORKDIR}"; }
trap cleanup EXIT

payload_start_offset() {
  local marker_line payload_offset skip_bytes byte_hex
  marker_line="$(awk '/^__PAYLOAD_BELOW__$/ { print NR; exit }' "$0")"
  [[ -n "${marker_line}" ]] || die "Payload marker not found"
  payload_offset="$(( $(head -n "${marker_line}" "$0" | wc -c | tr -d ' ') + 1 ))"
  skip_bytes=0
  while :; do
    byte_hex="$(dd if="$0" bs=1 skip="$((payload_offset + skip_bytes - 1))" count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')"
    case "${byte_hex}" in
      0a|0d) skip_bytes=$((skip_bytes + 1)) ;;
      "") die "Payload is empty" ;;
      *) break ;;
    esac
  done
  printf '%s\n' "$((payload_offset + skip_bytes))"
}

extract_payload() {
  rm -rf "${WORKDIR}"
  mkdir -p "${PAYLOAD_DIR}"
  tail -c +"$(payload_start_offset)" "$0" | tar -xzf - -C "${PAYLOAD_DIR}" || die "Failed to extract payload"
  [[ -f "${IMAGE_INDEX}" ]] || die "Payload is missing images/image-index.tsv"
  [[ -f "${MANIFEST_TEMPLATE}" ]] || die "Payload is missing manifests/sbgw.yaml.tmpl"
}

confirm() {
  if [[ "${YES}" == "true" ]]; then return 0; fi
  cat <<EOF_CONFIRM
Action: ${ACTION}
Namespace: ${NAMESPACE}
Name: ${INSTALL_NAME}
Registry: ${REGISTRY}
Upstream: ${UPSTREAM_BASE_URL}
Auth enabled: ${AUTH_ENABLED}
Skip image prepare: ${SKIP_IMAGE_PREPARE}
Apply manifests: ${APPLY}
EOF_CONFIRM
  read -r -p "Continue? [y/N] " ans
  case "${ans}" in y|Y|yes|YES) ;; *) die "Cancelled" ;; esac
}

registry_host() {
  local ref="$1"
  printf '%s\n' "${ref%%/*}"
}

image_basename() {
  local ref="$1"
  printf '%s\n' "${ref##*/}"
}

retarget_image() {
  local default_ref="$1"
  if [[ -z "${REGISTRY}" ]]; then
    printf '%s\n' "${default_ref}"
  else
    printf '%s/%s\n' "${REGISTRY%/}" "$(image_basename "${default_ref}")"
  fi
}

IMAGE_TO_DEPLOY=""
prepare_images() {
  if [[ "${SKIP_IMAGE_PREPARE}" == "true" ]]; then
    log "Skipping image prepare"
    local first_default
    first_default="$(awk -F'|' 'NR==2 {print $4}' "${IMAGE_INDEX}")"
    IMAGE_TO_DEPLOY="$(retarget_image "${first_default}")"
    return 0
  fi
  command -v docker >/dev/null 2>&1 || die "docker is required unless --skip-image-prepare is set"

  if [[ -n "${REGISTRY_USER}" || -n "${REGISTRY_PASS}" ]]; then
    [[ -n "${REGISTRY_USER}" && -n "${REGISTRY_PASS}" ]] || die "--registry-user and --registry-pass must be provided together"
    log "Docker login $(registry_host "${REGISTRY}")"
    printf '%s' "${REGISTRY_PASS}" | docker login "$(registry_host "${REGISTRY}")" -u "${REGISTRY_USER}" --password-stdin
  fi

  local line name tar_name load_ref default_ref platform pull dockerfile target_ref
  while IFS='|' read -r name tar_name load_ref default_ref platform pull dockerfile; do
    [[ "${name}" == "name" ]] && continue
    [[ -n "${tar_name}" ]] || continue
    [[ -f "${PAYLOAD_DIR}/images/${tar_name}" ]] || die "Image tar missing: ${tar_name}"
    target_ref="$(retarget_image "${default_ref}")"
    log "Loading image ${tar_name}"
    docker load -i "${PAYLOAD_DIR}/images/${tar_name}"
    log "Tag ${load_ref} -> ${target_ref}"
    docker tag "${load_ref}" "${target_ref}"
    log "Push ${target_ref}"
    docker push "${target_ref}"
    if [[ -z "${IMAGE_TO_DEPLOY}" ]]; then IMAGE_TO_DEPLOY="${target_ref}"; fi
  done < "${IMAGE_INDEX}"
}

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'
}

render_manifest() {
  [[ -n "${IMAGE_TO_DEPLOY}" ]] || die "IMAGE_TO_DEPLOY is empty"
  local node_port_line=""
  if [[ "${SERVICE_TYPE}" == "NodePort" && -n "${NODE_PORT}" ]]; then
    node_port_line="nodePort: ${NODE_PORT}"
  fi
  sed \
    -e "s|{{NAMESPACE}}|$(escape_sed "${NAMESPACE}")|g" \
    -e "s|{{APP_NAME}}|$(escape_sed "${INSTALL_NAME}")|g" \
    -e "s|{{IMAGE}}|$(escape_sed "${IMAGE_TO_DEPLOY}")|g" \
    -e "s|{{REPLICAS}}|$(escape_sed "${REPLICAS}")|g" \
    -e "s|{{SERVICE_TYPE}}|$(escape_sed "${SERVICE_TYPE}")|g" \
    -e "s|{{NODE_PORT_LINE}}|$(escape_sed "${node_port_line}")|g" \
    -e "s|{{UPSTREAM_BASE_URL}}|$(escape_sed "${UPSTREAM_BASE_URL}")|g" \
    -e "s|{{AUTH_ENABLED}}|$(escape_sed "${AUTH_ENABLED}")|g" \
    -e "s|{{AUTH_TOKEN}}|$(escape_sed "${AUTH_TOKEN}")|g" \
    "${MANIFEST_TEMPLATE}" > "${RENDERED_MANIFEST}"
  ok "Rendered manifest: ${RENDERED_MANIFEST}"
}

apply_manifest() {
  if [[ "${DRY_RUN}" == "true" ]]; then
    cat "${RENDERED_MANIFEST}"
    return 0
  fi
  if [[ "${APPLY}" != "true" ]]; then
    log "--no-apply set, manifest is rendered only: ${RENDERED_MANIFEST}"
    return 0
  fi
  command -v kubectl >/dev/null 2>&1 || die "kubectl is required unless --no-apply or --dry-run is set"
  kubectl apply -f "${RENDERED_MANIFEST}"
  kubectl -n "${NAMESPACE}" rollout status deploy/"${INSTALL_NAME}" --timeout="${WAIT_TIMEOUT}" || true
}

status() {
  command -v kubectl >/dev/null 2>&1 || die "kubectl is required for status"
  kubectl -n "${NAMESPACE}" get deploy,svc,cm -l app.kubernetes.io/name="${INSTALL_NAME}" -o wide
}

uninstall() {
  command -v kubectl >/dev/null 2>&1 || die "kubectl is required for uninstall"
  kubectl -n "${NAMESPACE}" delete deploy,svc,cm -l app.kubernetes.io/name="${INSTALL_NAME}" --ignore-not-found
  if [[ "${DELETE_NAMESPACE}" == "true" ]]; then
    kubectl delete namespace "${NAMESPACE}" --ignore-not-found
  fi
}

case "${ACTION}" in
  install)
    extract_payload
    confirm
    prepare_images
    render_manifest
    apply_manifest
    ok "Install completed"
    ;;
  status)
    status
    ;;
  uninstall)
    confirm
    uninstall
    ok "Uninstall completed"
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    usage
    die "Unknown action: ${ACTION}"
    ;;
esac

exit 0
__PAYLOAD_BELOW__
