#!/usr/bin/env bash
# ==============================================================================
# port-forward.sh: Centralized port-forward management for LLM Telemetry Gateway
# ==============================================================================
# Usage:
#   ./scripts/port-forward.sh [COMMAND] [TARGET]
#
# Commands:
#   start [TARGET]  Start port-forwards in the background and wait until ready.
#   stop            Stop background port-forwards tracked in the PID file.
#   run [TARGET]    Run port-forwards in the foreground (blocks until Ctrl+C / SIGINT).
#   status          Check and report whether target service ports are currently open.
#   help            Display usage instructions and exit.
#
# Targets:
#   gateway         Gateway completions proxy and web console (8080)
#   telemetry       Grafana (3000), Prometheus (9090)
#   ollama          Ollama local inference engine (11434)
#   all             All services across gateway, telemetry, and ollama (default)
#
# Flags:
#   -h, --help      Display usage information and exit.
# ==============================================================================
set -euo pipefail

PID_FILE="${TMPDIR:-/tmp}/llm-gateway-pf.pid"

is_port_open() {
  local port=$1
  (echo > /dev/tcp/127.0.0.1/"${port}") >/dev/null 2>&1
}

wait_for_port() {
  local port=$1
  local retries=20
  while ! is_port_open "${port}"; do
    retries=$((retries - 1))
    if [[ ${retries} -le 0 ]]; then
      echo "  [-] Timed out waiting for localhost:${port}" >&2
      return 1
    fi
    sleep 0.25
  done
}

forward_port() {
  local ns="$1"
  local target="$2"
  local local_port="$3"
  local remote_port="$4"
  local label="$5"

  if is_port_open "${local_port}"; then
    echo "  [*] ${label} already accessible on localhost:${local_port}"
    return 0
  fi

  echo "  [+] Forwarding ${label} (localhost:${local_port} -> ${ns}/${target}:${remote_port})..."
  kubectl port-forward -n "${ns}" "${target}" "${local_port}:${remote_port}" >/dev/null 2>&1 &
  local pid=$!
  echo "${pid}" >> "${PID_FILE}"
  wait_for_port "${local_port}"
}

stop_forwards() {
  if [[ -f "${PID_FILE}" ]]; then
    echo "[+] Stopping managed port-forward processes..."
    while IFS= read -r pid; do
      if [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null; then
        kill "${pid}" 2>/dev/null || true
      fi
    done < "${PID_FILE}"
    rm -f "${PID_FILE}"
    echo "[+] Managed port-forwards stopped."
  else
    echo "[*] No active managed port-forwards found in ${PID_FILE}."
  fi
}

start_targets() {
  local target="${1:-all}"
  case "${target}" in
    gateway)
      echo "[+] Starting Gateway port-forward..."
      forward_port "gateway" "deploy/gateway" 8080 8080 "Gateway & Console"
      ;;
    telemetry)
      echo "[+] Starting Telemetry port-forwards..."
      forward_port "telemetry" "svc/prometheus" 9090 9090 "Prometheus"
      forward_port "telemetry" "svc/grafana" 3000 3000 "Grafana"
      ;;
    ollama)
      echo "[+] Starting Ollama port-forward..."
      forward_port "ollama" "svc/ollama" 11434 11434 "Ollama LLM"
      ;;
    all)
      echo "[+] Starting all port-forwards..."
      forward_port "gateway" "deploy/gateway" 8080 8080 "Gateway & Console"
      forward_port "telemetry" "svc/prometheus" 9090 9090 "Prometheus"
      forward_port "telemetry" "svc/grafana" 3000 3000 "Grafana"
      forward_port "ollama" "svc/ollama" 11434 11434 "Ollama LLM"
      ;;
    *)
      echo "Unknown target: ${target}. Options: gateway, telemetry, ollama, all." >&2
      exit 1
      ;;
  esac
}

run_foreground() {
  local target="${1:-all}"
  trap stop_forwards EXIT INT TERM

  start_targets "${target}"

  echo ""
  echo "Port-forward session active (Press Ctrl+C to terminate):"
  if [[ "${target}" =~ ^(gateway|all)$ ]]; then
    echo "  - Gateway & Console: http://localhost:8080/console"
  fi
  if [[ "${target}" =~ ^(telemetry|all)$ ]]; then
    echo "  - Grafana:           http://localhost:3000 (admin/admin)"
    echo "  - Prometheus:        http://localhost:9090"
  fi
  if [[ "${target}" =~ ^(ollama|all)$ ]]; then
    echo "  - Ollama Engine:     http://localhost:11434"
  fi
  echo ""

  # Keep foreground process alive until signal
  while true; do
    sleep 1
  done
}

status_forwards() {
  echo "Checking endpoint statuses:"
  local ports=(
    "8080:Gateway & Console"
    "3000:Grafana"
    "9090:Prometheus"
    "11434:Ollama LLM"
  )
  for entry in "${ports[@]}"; do
    local port="${entry%%:*}"
    local name="${entry##*:}"
    if is_port_open "${port}"; then
      printf "  [OPEN]   %-20s (localhost:%s)\n" "${name}" "${port}"
    else
      printf "  [CLOSED] %-20s (localhost:%s)\n" "${name}" "${port}"
    fi
  done
}

# Command dispatch
CMD="${1:-run}"
TARGET="${2:-all}"

case "${CMD}" in
  start)
    start_targets "${TARGET}"
    ;;
  stop)
    stop_forwards
    ;;
  run)
    run_foreground "${TARGET}"
    ;;
  status)
    status_forwards
    ;;
  help|--help|-h)
    echo "Usage: $0 [start|stop|run|status] [gateway|telemetry|ollama|all]"
    echo ""
    echo "Commands:"
    echo "  start [target]  Start port-forwards in the background and wait until ready"
    echo "  stop            Stop background port-forwards tracked by this script"
    echo "  run [target]    Run port-forwards in the foreground (Ctrl+C to stop)"
    echo "  status          Check if service ports are currently open"
    echo ""
    echo "Targets:"
    echo "  gateway         Gateway & Console (8080)"
    echo "  telemetry       Grafana (3000), Prometheus (9090)"
    echo "  ollama          Ollama Local LLM (11434)"
    echo "  all             All services (default)"
    ;;
  *)
    echo "Unknown command: ${CMD}. Run '$0 help' for usage." >&2
    exit 1
    ;;
esac
