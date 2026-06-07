# Triggering and Verifying AIOps Diagnostics

This guide outlines how to manually inject chaos scenarios in the local cluster to trigger the sidecar's telemetry-driven anomaly detection and view the generated LLM Root Cause Analysis (RCA) reports.

---

## 📋 Prerequisites

Ensure all core namespaces and workloads are active and healthy:

```bash
kubectl get pods -A
```

The system requires:

- **`telemetry` Namespace**: OTel Collector, Node Exporter, and Prometheus running.
- **`ollama` Namespace**: Ollama container active with the `qwen2.5:0.5b` model loaded.
- **`gateway` Namespace**: The Go completions proxy and Python policy sidecar running in the `gateway` pod.

---

## 🚀 Step 1: Inject Chaos

You can simulate different incident scenarios by applying different Chaos Mesh manifests.

### Option A: Resource Starvation / Node Stress

Simulate host-level CPU and Memory starvation:

```bash
kubectl apply -f k3s/chaos-mesh/node-stress.yaml
```

- **Active Metric Anomalies**: Spikes host CPU/Memory beyond configured thresholds (`5.0%` CPU, `60.0%` Memory in the sandbox VM).
- **Expected RCA Diagnosis**: Ollama detects the utilization anomalies and identifies the scenario as **Resource Starvation / Stress**.

### Option B: Network Latency / Delay

Simulate network latency on outbound completions traffic:

```bash
# 1. Apply network delay chaos manifest
kubectl apply -f k3s/chaos-mesh/network-delay.yaml

# 2. Generate traffic through the completions gateway to trigger latency metrics
make test-k3s
```

- **Active Metric Anomalies**: Pushes gateway completions request latency beyond the `200ms` threshold (injects `300ms` delay).
- **Expected RCA Diagnosis**: Ollama flags the latency spike in the logs and identifies the scenario as **Network Delay**.

---

## 🔍 Step 2: Monitor Anomaly Detection

Check the sidecar logs to verify that the telemetry loop registers the metrics spike:

```bash
kubectl logs -n gateway deploy/gateway -c sidecar --tail=50
```

*Note: If there are multiple pods, target the active pod directly:*

```bash
kubectl logs -n gateway pod/<gateway-pod-name> -c sidecar --tail=50
```

---

## 📊 Step 3: View RCA Diagnostics

The sidecar compiles the diagnostics metrics and queries the local Ollama LLM asynchronously. You can inspect the compiled metrics context block:

```bash
kubectl exec -n gateway deploy/gateway -c sidecar -- cat /tmp/shared/diagnostics.txt
```

To view the generated natural-language RCA report, read the diagnostics log file:

```bash
kubectl exec -n gateway deploy/gateway -c sidecar -- cat /tmp/shared/rca.log
```

---

## 🧹 Step 4: Clean Up & Recover

Once verified, remove the active chaos policies to restore system metrics to nominal levels:

```bash
# Remove Resource Stress
kubectl delete -f k3s/chaos-mesh/node-stress.yaml

# Remove Network Delay
kubectl delete -f k3s/chaos-mesh/network-delay.yaml
```
