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
SERVICE_TYPE="NodePort"
NODE_PORT="30088"
UPSTREAM_BASE_URL="http://vllm:8000"
UPSTREAM_API_KEY=""
UPSTREAM_FORWARD_CLIENT_AUTHORIZATION="false"
UPSTREAM_STRATEGY="weighted_round_robin"
AUTH_ENABLED="true"
AUTH_TOKEN_SET="false"
AUTH_TOKENS=("sk-local-dev-001")
AUTH_KEYS=()
CONFIG_FILE=""
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
  --service-type <type>          ClusterIP|NodePort|LoadBalancer. Default: NodePort
  --node-port <port>             NodePort when service-type=NodePort. Default: 30088
  --upstream-base-url <url>      Upstream OpenAI-compatible base URL
  --upstream-api-key <sk>        Upstream API key. Empty means upstream does not need key.
  --forward-client-authorization true|false
                                  Forward client Authorization only when upstream-api-key is empty. Default: false
  --upstream-strategy <strategy> round_robin|weighted_round_robin|random|weighted_random|least_inflight
  --config-file <path>          Use a full config.yaml. Useful for routes/subpaths/multiple upstreams.
  --auth-enabled true|false      Enable gateway SK auth. Default: true
  --auth-token <sk>              Gateway SK token. Can be repeated. First usage replaces default token.
  --auth-tokens <a,b,c>          Comma separated gateway SK tokens. Replaces default token.
  --auth-key <name:key[:quota]>  Gateway key with optional token quota. Can be repeated.
  --wait-timeout <duration>      kubectl rollout wait timeout. Default: 120s
  --no-apply                     Render manifest only, do not kubectl apply
  --dry-run                      Print rendered manifest, do not apply
  --delete-namespace             uninstall also deletes namespace
  -y, --yes                      Skip confirmation

Examples:
  ./sbgw-v0.1.0-linux-amd64.run install \
    --registry sealos.hub:5000/kube4 \
    --upstream-base-url http://qwen-vllm.aict.svc:8000 \
    --upstream-api-key sk-upstream-xxx \
    --auth-key user-a:sk-user-a:1000000 \
    --auth-key user-b:sk-user-b:500000 \
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

set_auth_tokens_from_csv() {
  AUTH_TOKENS=()
  AUTH_TOKEN_SET="true"
  local csv="$1" part
  IFS=',' read -r -a parts <<< "${csv}"
  for part in "${parts[@]}"; do
    part="$(printf '%s' "${part}" | sed -e 's/^ *//' -e 's/ *$//')"
    [[ -n "${part}" ]] && AUTH_TOKENS+=("${part}")
  done
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
    --upstream-api-key) UPSTREAM_API_KEY="${2:-}"; shift 2 ;;
    --forward-client-authorization) UPSTREAM_FORWARD_CLIENT_AUTHORIZATION="$(parse_bool "${2:-}")"; shift 2 ;;
    --upstream-strategy) UPSTREAM_STRATEGY="${2:-}"; shift 2 ;;
    --config-file) CONFIG_FILE="${2:-}"; shift 2 ;;
    --auth-enabled) AUTH_ENABLED="$(parse_bool "${2:-}")"; shift 2 ;;
    --auth-token)
      if [[ "${AUTH_TOKEN_SET}" != "true" ]]; then AUTH_TOKENS=(); AUTH_TOKEN_SET="true"; fi
      AUTH_TOKENS+=("${2:-}"); shift 2 ;;
    --auth-tokens) set_auth_tokens_from_csv "${2:-}"; shift 2 ;;
    --auth-key) AUTH_KEYS+=("${2:-}"); shift 2 ;;
    --wait-timeout) WAIT_TIMEOUT="${2:-}"; shift 2 ;;
    --no-apply) APPLY="false"; shift ;;
    --dry-run) DRY_RUN="true"; APPLY="false"; shift ;;
    --delete-namespace) DELETE_NAMESPACE="true"; shift ;;
    -y|--yes) YES="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "Unknown argument: $1" ;;
  esac
done

case "${SERVICE_TYPE}" in
  ClusterIP|NodePort|LoadBalancer) ;;
  *) die "--service-type must be ClusterIP, NodePort or LoadBalancer" ;;
esac
case "${UPSTREAM_STRATEGY}" in
  round_robin|weighted_round_robin|random|weighted_random|least_inflight) ;;
  *) die "Unsupported --upstream-strategy: ${UPSTREAM_STRATEGY}" ;;
esac
if [[ -n "${CONFIG_FILE}" && ! -f "${CONFIG_FILE}" ]]; then
  die "--config-file not found: ${CONFIG_FILE}"
fi

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
Service: ${SERVICE_TYPE}${NODE_PORT:+/${NODE_PORT}}
Upstream: ${UPSTREAM_BASE_URL}
Upstream API key: $( [[ -n "${UPSTREAM_API_KEY}" ]] && printf '<set>' || printf '<empty>' )
Upstream strategy: ${UPSTREAM_STRATEGY}
Auth enabled: ${AUTH_ENABLED}
Gateway tokens: ${#AUTH_TOKENS[@]}
Gateway quota keys: ${#AUTH_KEYS[@]}
Config file: $( [[ -n "${CONFIG_FILE}" ]] && printf "%s" "${CONFIG_FILE}" || printf "<generated>" )
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

yaml_escape() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

render_tokens_block() {
  if [[ ${#AUTH_TOKENS[@]} -eq 0 ]]; then
    printf ' []'
    return 0
  fi
  printf '\n'
  local token
  for token in "${AUTH_TOKENS[@]}"; do
    [[ -z "${token}" ]] && continue
    printf '        - "%s"\n' "$(yaml_escape "${token}")"
  done
}

render_keys_block() {
  if [[ ${#AUTH_KEYS[@]} -eq 0 ]]; then
    printf ' []'
    return 0
  fi
  printf '\n'
  local entry name key quota rest
  for entry in "${AUTH_KEYS[@]}"; do
    name="${entry%%:*}"
    rest="${entry#*:}"
    [[ "${rest}" != "${entry}" ]] || die "--auth-key format must be name:key[:quota]"
    key="${rest%%:*}"
    quota="0"
    if [[ "${rest}" == *:* ]]; then quota="${rest#*:}"; fi
    [[ -n "${name}" && -n "${key}" ]] || die "--auth-key format must be name:key[:quota]"
    [[ "${quota}" =~ ^[0-9]+$ ]] || die "--auth-key quota must be an integer: ${entry}"
    printf '        - name: "%s"\n' "$(yaml_escape "${name}")"
    printf '          key: "%s"\n' "$(yaml_escape "${key}")"
    printf '          quota_tokens: %s\n' "${quota}"
    printf '          disabled: false\n'
  done
}

render_custom_config_manifest() {
  local input_manifest="$1" output_manifest="$2" config_file="$3"
  awk -v config_file="${config_file}" '
    BEGIN {
      while ((getline line < config_file) > 0) {
        cfg = cfg "    " line "\n"
      }
      close(config_file)
    }
    $0 == "  config.yaml: |" {
      print
      printf "%s", cfg
      skip = 1
      next
    }
    skip && $0 == "---" {
      skip = 0
      print
      next
    }
    skip { next }
    { print }
  ' "${input_manifest}" > "${output_manifest}"
}

render_manifest() {
  [[ -n "${IMAGE_TO_DEPLOY}" ]] || die "IMAGE_TO_DEPLOY is empty"
  local node_port_line=""
  if [[ "${SERVICE_TYPE}" == "NodePort" ]]; then
    [[ -n "${NODE_PORT}" ]] || NODE_PORT="30088"
    node_port_line="nodePort: ${NODE_PORT}"
  fi
  local auth_tokens_block auth_keys_block
  auth_tokens_block="$(render_tokens_block)"
  auth_keys_block="$(render_keys_block)"

  awk \
    -v NAMESPACE="${NAMESPACE}" \
    -v APP_NAME="${INSTALL_NAME}" \
    -v IMAGE="${IMAGE_TO_DEPLOY}" \
    -v REPLICAS="${REPLICAS}" \
    -v SERVICE_TYPE="${SERVICE_TYPE}" \
    -v NODE_PORT_LINE="${node_port_line}" \
    -v UPSTREAM_BASE_URL="${UPSTREAM_BASE_URL}" \
    -v UPSTREAM_API_KEY="${UPSTREAM_API_KEY}" \
    -v UPSTREAM_FORWARD_CLIENT_AUTHORIZATION="${UPSTREAM_FORWARD_CLIENT_AUTHORIZATION}" \
    -v UPSTREAM_STRATEGY="${UPSTREAM_STRATEGY}" \
    -v AUTH_ENABLED="${AUTH_ENABLED}" \
    -v AUTH_TOKENS_BLOCK="${auth_tokens_block}" \
    -v AUTH_KEYS_BLOCK="${auth_keys_block}" \
    '
    function repl(s, old, new, out, i) {
      out=""
      while ((i=index(s, old)) > 0) {
        out = out substr(s, 1, i-1) new
        s = substr(s, i + length(old))
      }
      return out s
    }
    {
      line=$0
      line=repl(line,"{{NAMESPACE}}",NAMESPACE)
      line=repl(line,"{{APP_NAME}}",APP_NAME)
      line=repl(line,"{{IMAGE}}",IMAGE)
      line=repl(line,"{{REPLICAS}}",REPLICAS)
      line=repl(line,"{{SERVICE_TYPE}}",SERVICE_TYPE)
      line=repl(line,"{{NODE_PORT_LINE}}",NODE_PORT_LINE)
      line=repl(line,"{{UPSTREAM_BASE_URL}}",UPSTREAM_BASE_URL)
      line=repl(line,"{{UPSTREAM_API_KEY}}",UPSTREAM_API_KEY)
      line=repl(line,"{{UPSTREAM_FORWARD_CLIENT_AUTHORIZATION}}",UPSTREAM_FORWARD_CLIENT_AUTHORIZATION)
      line=repl(line,"{{UPSTREAM_STRATEGY}}",UPSTREAM_STRATEGY)
      line=repl(line,"{{AUTH_ENABLED}}",AUTH_ENABLED)
      line=repl(line,"{{AUTH_TOKENS_BLOCK}}",AUTH_TOKENS_BLOCK)
      line=repl(line,"{{AUTH_KEYS_BLOCK}}",AUTH_KEYS_BLOCK)
      print line
    }' "${MANIFEST_TEMPLATE}" > "${RENDERED_MANIFEST}"
  if [[ -n "${CONFIG_FILE}" ]]; then
    local tmp_manifest="${RENDERED_MANIFEST}.tmp"
    render_custom_config_manifest "${RENDERED_MANIFEST}" "${tmp_manifest}" "${CONFIG_FILE}"
    mv "${tmp_manifest}" "${RENDERED_MANIFEST}"
  fi
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
