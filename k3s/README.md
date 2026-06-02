# Kubernetes Local Infrastructure (k3s)

This directory houses the complete declarative Infrastructure-as-Code (IaC) manifests for the LLM Telemetry Gateway. The cluster architecture runs on a single-node local `k3s` cluster nested inside a Fedora Silverblue Toolbox environment, using host-volume mounts for development speed.

---

## 📂 Directory Layout

The resources are organized into domain-driven subdirectories to enforce logical separation of concerns, path-based CI/CD permissions, and layered bootstrapping:

```
k3s/
├── bootstrap/
│   ├── 01-namespace.yaml              # Core cluster namespaces (gateway, telemetry, ollama)
│   └── 02-limit-range-telemetry.yaml  # Resource bounds for telemetry namespace
├── apps/
│   └── deployment.yaml                # Co-located Go proxy & Python sidecar workload
├── telemetry/
│   ├── prometheus-infra.yaml          # Pinned Prometheus server configuration
│   └── telemetry.yaml                 # Pinned OTel Collector configuration
└── ollama/
    └── ollama.yaml                    # Pinned GPU-accelerated Ollama LLM environment
```

---

## 🛡️ Namespace & Resource Governance Design

To prevent local laptop resource starvation and ensure stable scheduling, the cluster implements a multi-namespace architecture with customized resource limits:

### 1. The `telemetry` Namespace & Centralized LimitRange
The telemetry stack (OTel Collector and Prometheus) is governed by a central `LimitRange` policy defined in `bootstrap/02-limit-range-telemetry.yaml`.
* **Centralized Defaults**: To adhere strictly to the DRY (Don't Repeat Yourself) principle, individual workload manifests inside `telemetry/` omit their `resources` blocks.
* **Automatic Injection**: The `LimitRange` dynamically injects a default request of **`100m CPU / 256Mi Memory`** and a default limit of **`500m CPU / 1Gi Memory`** to any container scheduled inside the namespace.
* **API Validation Gates**: It enforces hard boundaries (**`Min: 50m CPU / 64Mi Memory`**; **`Max: 1 CPU / 2Gi Memory`**). The Kubernetes API will immediately reject any workload that requests allocations outside this boundary.

### 2. The `ollama` Namespace & Sandboxing
The local LLM cognitive diagnostic engine is isolated into its own `ollama` namespace:
* **Separation of Concerns**: Because Ollama is a resource-intensive workload, keeping it in its own namespace prevents the telemetry `LimitRange` from throttling its execution.
* **CPU Execution**: Configured for host CPU execution, allowing 100% portable operations out-of-the-box in local developer environments without physical GPU drivers.
* **Resource Sandboxing**: Ollama is explicitly sandboxed to requests of **`2 CPU / 4Gi Memory`** and limits of **`4 CPU / 8Gi Memory`**, ensuring the LLM runtime has stable, isolated execution boundaries that prevent host node starvation.

---

## 🚀 Deterministic Bootstrap Sequence

To deploy the infrastructure without scheduling conflicts (ensuring logical envelopes and LimitRanges are active before containers schedule), apply the manifests in the following order:

### Phase 1: Establish Cluster Boundaries (Bootstrap)
Provision the namespaces and active resource limits:
```bash
kubectl apply -f k3s/bootstrap/
```

### Phase 2: Spin Up Infrastructure Core & Cognitive Layer
Deploy the telemetry routing pipelines and the LLM engine to allow them to load weights and initialize VRAM maps:
```bash
kubectl apply -f k3s/telemetry/
kubectl apply -f k3s/ollama/
```

### Phase 3: Deploy Application Workloads
Deploy the co-located dual-container data plane Pod:
```bash
kubectl apply -f k3s/apps/
```

---

## 📦 Pinned Container Matrix

To guarantee absolute build reproducibility and protect the sandbox from unexpected upstream regressions or breaking changes, floating image tags (like `:latest`) are banned. The platform standardizes on the following pinned stable and LTS tags:

| Component | Target Namespace | Image Reference | Lifecycle Phase / Purpose |
| :--- | :--- | :--- | :--- |
| **Go Proxy Proxy** | `gateway` | `alpine:3.20` | LTS base image, supported until May 2026. |
| **Python Sidecar** | `gateway` | `python:3.13-slim` | Stable minor runtime release, optimized slim build. |
| **OTel Collector** | `telemetry` | `otel/opentelemetry-collector-contrib:0.110.0` | Pinned stable minor release with Prometheus metrics export. |
| **Prometheus DB** | `telemetry` | `prom/prometheus:v2.51.2` | Stable long-term support branch database. |
| **Ollama LLM** | `ollama` | `ollama/ollama:0.5.1` | Pinned feature-stable release for local LLM inference. |
