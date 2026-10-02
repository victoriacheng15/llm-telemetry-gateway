#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# teardown.sh: Teardown local cluster resources, port-forwards, and namespaces
# ==============================================================================
# Flags & Arguments:
#   --with-chaos  Optionally uninstall Chaos Mesh release and namespace.
#   -h, --help    Display usage instructions and exit.
#
# Environment Overrides:
#   WITH_CHAOS    Uninstall Chaos Mesh (default: false)
#   GATEWAY_NS    Gateway namespace (default: "gateway")
#   TELEMETRY_NS  Telemetry namespace (default: "telemetry")
#   OLLAMA_NS     Ollama namespace (default: "ollama")
#   CHAOS_NS      Chaos Mesh namespace (default: "chaos-mesh")
#
# Usage:
#   bash scripts/teardown.sh [--with-chaos]
# ==============================================================================

WITH_CHAOS="${WITH_CHAOS:-false}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --with-chaos)
      WITH_CHAOS=true
      shift
      ;;
    -h|--help)
      grep '^#' "$0" | grep -v '^#!' | cut -c 3-
      exit 0
      ;;
    *)
      echo "Error: Unknown argument '$1'." >&2
      echo "Usage: $0 [--with-chaos] [-h|--help]" >&2
      exit 1
      ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATEWAY_NS="${GATEWAY_NS:-gateway}"
TELEMETRY_NS="${TELEMETRY_NS:-telemetry}"
OLLAMA_NS="${OLLAMA_NS:-ollama}"
CHAOS_NS="${CHAOS_NS:-chaos-mesh}"

echo "=== [1/4] Stopping Active Port-Forwards ==="
if [[ -f "${REPO_ROOT}/scripts/port-forward.sh" ]]; then
  bash "${REPO_ROOT}/scripts/port-forward.sh" stop || true
fi

echo "=== [2/4] Removing Active Chaos Experiments ==="
if command -v kubectl >/dev/null 2>&1; then
  kubectl delete -f "${REPO_ROOT}/k3s/chaos-mesh/scenarios/" --ignore-not-found -R 2>/dev/null || true
fi

if [[ "${WITH_CHAOS}" == "true" ]]; then
  echo "Uninstalling Chaos Mesh controller..."
  if command -v helm >/dev/null 2>&1 && helm status chaos-mesh -n "${CHAOS_NS}" >/dev/null 2>&1; then
    helm uninstall chaos-mesh -n "${CHAOS_NS}" || true
  fi
fi

echo "=== [3/4] Deleting Cluster Namespaces ==="
TARGET_NAMESPACES=("${GATEWAY_NS}" "${TELEMETRY_NS}" "${OLLAMA_NS}")
if [[ "${WITH_CHAOS}" == "true" ]]; then
  TARGET_NAMESPACES+=("${CHAOS_NS}")
fi

if command -v kubectl >/dev/null 2>&1; then
  for ns in "${TARGET_NAMESPACES[@]}"; do
    if kubectl get namespace "${ns}" >/dev/null 2>&1; then
      echo "Deleting namespace: ${ns}..."
      kubectl delete namespace "${ns}" --wait=false || true
    else
      echo "Namespace ${ns} does not exist."
    fi
  done

  echo "Waiting for namespaces to terminate..."
  for ns in "${TARGET_NAMESPACES[@]}"; do
    kubectl wait --for=delete "namespace/${ns}" --timeout=60s 2>/dev/null || true
  done
fi

echo "=== [4/4] Cleaning Local Build Artifacts ==="
rm -f "${REPO_ROOT}/bin/gateway"

echo ""
echo "================================================================="
echo " Teardown Complete! All local cluster resources removed."
echo "================================================================="
