# System Architecture & AIOps Diagnostics

This document details the core data plane design, Inter-Process Communication (IPC), reliability patterns, and AIOps cognitive diagnostics of the LLM Telemetry Gateway.

---

## 🗺️ Architectural Topology

The gateway intercepts completions traffic directed towards Large Language Models (LLMs), masks sensitive PII before transmission, and exports granular telemetry while running continuous, local AIOps diagnostics.

![LLM Telemetry Gateway Architecture](./assets/architecture.png)

---

## 🔌 Inter-Process Communication (IPC)

To minimize proxy latency overhead, the Go completions proxy and the Python policy sidecar communicate via a UNIX Domain Socket (UDS) located at `/tmp/shared/policy.sock`.

### Why Unix Domain Sockets?

- **Zero-Network Overhead**: Communicating over UDS bypasses the TCP loopback network stack entirely, eliminating kernel syscall overhead and reducing latency to sub-millisecond ranges.
- **Security Boundaries**: UDS communication is restricted to containers sharing the same local filesystem namespace. Under the `emptyDir` mount layout, socket access is isolated strictly within the boundary of the individual Pod.

### IPC Protocol

1. **Connection Lifecycle**:
   - The Python sidecar opens and binds to the socket path on startup.
   - The Go gateway acts as the client dialer, establishing a short-lived connection per completion request (configured with connection timeouts of `100ms`).
2. **Payload Design**:
   - Data is exchanged using newline-delimited JSON or raw JSON blocks.
   - Example Input: `{"prompt": "User SSN is 123-45-6789"}`
   - Example Output: `{"prompt": "User SSN is [REDACTED_SSN]"}`

---

## 🚦 Reliability Patterns

To guarantee resilience, the data plane incorporates two main safety frameworks:

### 1. Fail-Closed Mode

When security policies (like PII masking) are critical, letting an unmasked request pass upstream is an unacceptable compromise.

- **Behavior**: If the UDS connection times out, fails to connect, or returns an invalid status, the Go proxy triggers a **fail-closed** sequence.
- **Resolution**: The proxy aborts the request, blocks it from reaching the upstream model, and responds to the client immediately with an `HTTP 503 Service Unavailable` status.

### 2. Kubernetes Readiness Gates

To prevent routing traffic to a proxy instance before its sidecar is functional, the Go application exposes dual health endpoints:

- `/healthz`: Evaluates the liveness of the Go process itself (always returns `200 OK`).
- `/readyz`: Tests connectivity by dialing the sidecar socket `/tmp/shared/policy.sock`. If the socket cannot be dialed within `100ms`, `/readyz` fails with `503 Service Unavailable`, preventing the ingress from routing live traffic to the pod.

---

## 🤖 AIOps Diagnostics & Anomaly Detection

To facilitate intelligent incident identification, the Go completions proxy runs an integrated telemetry collection and diagnostics evaluation loop (`internal/gateway/system_metrics.go`).

### 1. Telemetry Ingestion Channels

The Go evaluator scrapes kernel and runtime metrics every 10 seconds:

- **Pod & Container Metrics**: Scrapes cgroups v2 (`/sys/fs/cgroup/cpu.stat`, `memory.current`) with automatic fallbacks to cgroups v1 (`cpuacct.usage`), `/proc/self/stat`, and Go runtime memory stats.
- **Service & Socket Health**: Tests Unix Domain Socket connectivity to `/tmp/shared/policy.sock` to detect sidecar dial timeouts and connection failures.
- **Request Latency & Error Rates**: Tracks rolling completion request durations and monitors HTTP `5xx` error occurrences.

### 2. Anomaly Classifications

An anomaly is triggered if any of the configured boundaries are crossed:

- **CPU Utilization**: Exceeds the configured threshold (default `80.0%`).
- **Memory RSS Utilization**: Crosses configured container limits or allocations.
- **HTTP Latency**: Gateway completion requests exceed the latency threshold (default `200ms`).
- **Socket Connectivity**: Sidecar UDS connection fails, drops, or times out.
- **HTTP Server Error**: Any gateway request returns a status code `>= 500`.

### 3. RCA Synthesis (Ollama Integration)

Upon anomaly detection, the Go evaluator constructs a structured diagnostic prompt containing active telemetry and asynchronously queries the local Ollama instance (`qwen2.5:0.5b`) running in the `ollama` namespace. Ollama synthesizes the telemetry patterns into an automated Root Cause Analysis (RCA) diagnosis, which is broadcast live via Server-Sent Events (SSE) to the Web Observability Console.

---

## 🧪 Triggering & Verifying AIOps Diagnostics

You can validate the full incident loop by injecting chaos scenarios into the cluster using Chaos Mesh.

### Prerequisites

Ensure all core namespaces and workloads are active and the Ollama model is loaded:

```bash
# Verify pods across namespaces
kubectl get pods -A

# Pull the model if deploying fresh
kubectl exec -n ollama deploy/ollama -- ollama pull qwen2.5:0.5b
```

### Injecting Chaos Scenarios

#### Scenario A: Resource Starvation / Node Stress

Simulate host-level CPU and Memory starvation:

```bash
kubectl apply -f k3s/chaos-mesh/scenarios/stress/cpu-stress.yaml
```

- **Active Metric Anomalies**: Spikes CPU/Memory beyond configured thresholds.
- **Expected RCA Diagnosis**: Ollama detects utilization spikes and reports resource starvation.

#### Scenario B: Network Latency / Delay

Simulate network latency on outbound completions traffic:

```bash
# Apply network delay manifest
kubectl apply -f k3s/chaos-mesh/scenarios/network/network-delay.yaml

# Generate synthetic traffic to trigger latency thresholds
make test-k3s
```

- **Active Metric Anomalies**: Pushes gateway completion latency beyond `200ms`.
- **Expected RCA Diagnosis**: Ollama attributes the anomaly to network delay.

### Viewing RCA Diagnoses

- **Web Observability Console**: Open `http://localhost:3000` to view live SSE event streams, charts, and natural-language RCA summaries.
- **CLI Logs**: Tail gateway logs directly:

  ```bash
  kubectl logs -n gateway -l app=llm-telemetry-gateway -c gateway -f
  ```

### Teardown & Chaos Recovery

Remove the active chaos experiments to restore normal operational state:

```bash
kubectl delete -f k3s/chaos-mesh/scenarios/stress/cpu-stress.yaml
kubectl delete -f k3s/chaos-mesh/scenarios/network/network-delay.yaml
```
