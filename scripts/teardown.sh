#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# teardown.sh: Teardown local cluster resources, port-forwards, and namespaces
# ==============================================================================
# Flags & Arguments:
#   None (runs teardown against current kubectl context).
#   -h, --help    Display usage instructions and exit.
#
# Environment Overrides:
#   GATEWAY_NS    Gateway namespace (default: "gateway")
#   TELEMETRY_NS  Telemetry namespace (default: "telemetry")
#   OLLAMA_NS     Ollama namespace (default: "ollama")
#
# Usage:
#   bash scripts/teardown.sh
# ==============================================================================

if [[ "${1:-}" =~ ^(-h|--help)$ ]]; then
  grep '^#' "$0" | grep -v '^#!' | cut -c 3-
  exit 0
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATEWAY_NS="${GATEWAY_NS:-gateway}"
TELEMETRY_NS="${TELEMETRY_NS:-telemetry}"
OLLAMA_NS="${OLLAMA_NS:-ollama}"

echo "=== [1/4] Stopping Active Port-Forwards ==="
if [[ -f "${REPO_ROOT}/scripts/port-forward.sh" ]]; then
  bash "${REPO_ROOT}/scripts/port-forward.sh" stop || true
fi

echo "=== [2/4] Removing Active Chaos Experiments ==="
if command -v kubectl >/dev/null 2>&1; then
  kubectl delete -f "${REPO_ROOT}/k3s/chaos-mesh/scenarios/" --ignore-not-found -R 2>/dev/null || true
fi

echo "=== [3/4] Deleting Cluster Namespaces ==="
if command -v kubectl >/dev/null 2>&1; then
  for ns in "${GATEWAY_NS}" "${TELEMETRY_NS}" "${OLLAMA_NS}"; do
    if kubectl get namespace "${ns}" >/dev/null 2>&1; then
      echo "Deleting namespace: ${ns}..."
      kubectl delete namespace "${ns}" --wait=false || true
    else
      echo "Namespace ${ns} does not exist."
    fi
  done

  echo "Waiting for namespaces to terminate..."
  for ns in "${GATEWAY_NS}" "${TELEMETRY_NS}" "${OLLAMA_NS}"; do
    kubectl wait --for=delete "namespace/${ns}" --timeout=60s 2>/dev/null || true
  done
fi

echo "=== [4/4] Cleaning Local Build Artifacts ==="
rm -f "${REPO_ROOT}/bin/gateway"

echo ""
echo "================================================================="
echo " Teardown Complete! All local cluster resources removed."
echo "================================================================="
