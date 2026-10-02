#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# bootstrap.sh: Idempotent local cluster bootstrap and stack provisioning
# ==============================================================================
# Flags & Arguments:
#   --with-chaos  Optionally install Chaos Mesh controller using Helm.
#   -h, --help    Display usage instructions and exit.
#
# Environment Overrides:
#   WITH_CHAOS    Enable Chaos Mesh installation (default: false)
#   GATEWAY_NS    Gateway namespace (default: "gateway")
#   TELEMETRY_NS  Telemetry namespace (default: "telemetry")
#   OLLAMA_NS     Ollama namespace (default: "ollama")
#   CHAOS_NS      Chaos Mesh namespace (default: "chaos-mesh")
#   OLLAMA_MODEL  Local LLM diagnostic model (default: "qwen2.5:0.5b")
#
# Usage:
#   bash scripts/bootstrap.sh [--with-chaos]
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
OLLAMA_MODEL="${OLLAMA_MODEL:-qwen2.5:0.5b}"

echo "=== [1/7] Preflight Checks ==="
command -v kubectl >/dev/null 2>&1 || { echo "Error: kubectl is required but not installed." >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo "Error: go is required but not installed." >&2; exit 1; }
command -v sed >/dev/null 2>&1 || { echo "Error: sed is required but not installed." >&2; exit 1; }

if [[ "${WITH_CHAOS}" == "true" ]]; then
  command -v helm >/dev/null 2>&1 || { echo "Error: helm is required for Chaos Mesh installation." >&2; exit 1; }
fi

echo "Kubernetes context: $(kubectl config current-context)"

echo "=== [2/7] Compiling Go Gateway Binary ==="
echo "Building static gateway binary for container hostPath mount..."
mkdir -p "${REPO_ROOT}/bin"
CGO_ENABLED=0 go build -ldflags "-extldflags -static" -o "${REPO_ROOT}/bin/gateway" "${REPO_ROOT}/cmd/gateway/main.go"

echo "=== [3/7] Provisioning Bootstrap Namespaces & Limits ==="
kubectl apply -f "${REPO_ROOT}/k3s/bootstrap/"

echo "=== [4/7] Deploying Telemetry Stack & Provisioning Dashboards ==="
if [[ -f "${REPO_ROOT}/k3s/telemetry/dashboards/dashboard.json" ]]; then
  echo "Importing Grafana dashboard (dashboard.json)..."
  kubectl create configmap grafana-dashboard-json \
    --from-file=dashboard.json="${REPO_ROOT}/k3s/telemetry/dashboards/dashboard.json" \
    --namespace="${TELEMETRY_NS}" \
    --dry-run=client -o yaml | kubectl apply -f -
fi
kubectl apply -f "${REPO_ROOT}/k3s/telemetry/"

echo "=== [5/7] Deploying Ollama LLM Runtime ==="
kubectl apply -f "${REPO_ROOT}/k3s/ollama/"

echo "=== [6/7] Deploying Gateway Proxy Workload ==="
kubectl apply -f "${REPO_ROOT}/k3s/apps/rbac.yaml"
kubectl apply -f "${REPO_ROOT}/k3s/apps/network-policy.yaml"
sed "s|/opt/llm-telemetry-gateway|${REPO_ROOT}|g" "${REPO_ROOT}/k3s/apps/deployment.yaml" | kubectl apply -f -

echo "Waiting for core deployments to reach ready state..."
kubectl rollout status deployment/ollama -n "${OLLAMA_NS}" --timeout=180s
kubectl rollout status deployment/prometheus -n "${TELEMETRY_NS}" --timeout=120s
kubectl rollout status deployment/otel-collector -n "${TELEMETRY_NS}" --timeout=120s
kubectl rollout status deployment/grafana -n "${TELEMETRY_NS}" --timeout=120s
kubectl rollout status deployment/gateway -n "${GATEWAY_NS}" --timeout=120s

if [[ "${WITH_CHAOS}" == "true" ]]; then
  echo "=== Deploying Chaos Mesh Controller ==="
  helm repo add chaos-mesh https://charts.chaos-mesh.org >/dev/null 2>&1 || true
  helm repo update chaos-mesh >/dev/null 2>&1 || helm repo update >/dev/null 2>&1
  helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh \
    --namespace "${CHAOS_NS}" \
    --create-namespace \
    --values "${REPO_ROOT}/k3s/chaos-mesh/values.yaml"

  echo "Waiting for Chaos Mesh controller to reach ready state..."
  kubectl rollout status deployment/chaos-controller-manager -n "${CHAOS_NS}" --timeout=180s
fi

echo "=== [7/7] Pre-warming Local Ollama Model ==="
echo "Checking model '${OLLAMA_MODEL}' in Ollama container..."
if kubectl exec -n "${OLLAMA_NS}" deploy/ollama -- ollama list 2>/dev/null | grep -q "${OLLAMA_MODEL}"; then
  echo "Model '${OLLAMA_MODEL}' is already cached in Ollama."
else
  echo "Pulling '${OLLAMA_MODEL}' into local Ollama runtime..."
  kubectl exec -n "${OLLAMA_NS}" deploy/ollama -- ollama pull "${OLLAMA_MODEL}"
fi

echo ""
echo "================================================================="
echo " Bootstrap Complete! LLM Telemetry Gateway is fully operational."
echo "================================================================="
echo "Active Pods in ${GATEWAY_NS}:"
kubectl get pods -n "${GATEWAY_NS}"
echo ""
echo "Active Pods in ${TELEMETRY_NS}:"
kubectl get pods -n "${TELEMETRY_NS}"
echo ""
echo "Active Pods in ${OLLAMA_NS}:"
kubectl get pods -n "${OLLAMA_NS}"

if [[ "${WITH_CHAOS}" == "true" ]]; then
  echo ""
  echo "Active Pods in ${CHAOS_NS}:"
  kubectl get pods -n "${CHAOS_NS}"
fi

echo ""
echo "Access Services via Port-Forwarding:"
echo "  Run all forwards: ./scripts/port-forward.sh run all (or make port-forward)"
echo "  Gateway & Console: http://localhost:8080/console"
echo "  Grafana Dashboard: http://localhost:3000 (admin/admin)"
echo "  Prometheus TSDB:   http://localhost:9090"
echo "  Ollama Local LLM:  http://localhost:11434"
if [[ "${WITH_CHAOS}" == "true" ]]; then
  echo "  Chaos Scenarios:   k3s/chaos-mesh/scenarios/"
fi
echo "================================================================="
