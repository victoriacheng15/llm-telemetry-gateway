# LLM Telemetry Gateway

LLM Telemetry Gateway is a self-hosted platform engineering lab built with Kubernetes, OpenTelemetry, Prometheus, Grafana, Ollama, Go completions proxy, and Python sidecar.

It proves an end-to-end platform ownership loop: declarative infrastructure runs completions and telemetry services, telemetry exposes pipeline behavior, sidecar policies intercept and mask prompts, and ADRs/RCAs preserve operational memory.

[Full Documentation](./docs/README.md)

---

## Architecture

The system processes requests and manages state through simplified operational paths:

| Path | Purpose | Flow |
| :--- | :--- | :--- |
| Infrastructure Sync | Align cluster and host state declaratively | `kubectl` -> `K3s` runtime |
| Telemetry Pipeline | Capture observability metrics and JSON logs | `Go Proxy` -> `OTel Collector` -> `Prometheus` |
| Policy Masking | Redact PII (SSNs, CCs) from LLM prompts | `Go Proxy` -> `UDS` -> `Python Sidecar` |
| Local Inference | Query LLM completions and execute diagnostics | `Go Proxy` -> `Ollama API` |
| Incident Memory | Document and preserve architectural learnings | `ADRs` / `RCAs` / `Incidents` |

```text
           ┌────────┐             ┌────────────┐
           │ Client │             │ Chaos Mesh │
           └────────┘             └────────────┘
                 │                       │
                 │ (Completions)         ├────────────────────────┐
                 ▼                       ▼ (Injects faults)       ▼ (Injects faults)
          ┌─────────────────────┐ <──────┘               ┌─────────────────────┐
          │ Gateway Proxy (Go)  │ <====================> │ Sidecar Policy (Py) │
          └─────────────────────┘      (UDS Socket)      └─────────────────────┘
                 │                                            │             │
                 │ (Sends metrics)           (Scrapes metrics)│             │ (RCA queries)
                 ▼                                            ▼             ▼
          ┌───────────┐ <─────────────────────────────────────┘      ┌────────────┐
          │ OTel      │                                              │ Ollama LLM │
          │ Collector │                                              └────────────┘
          └───────────┘
```

---

## Tech Stack

| Layer | Tools |
| :--- | :--- |
| Language | Go, Python |
| Infrastructure | Kubernetes (k3s), Docker |
| Observability | OpenTelemetry, Prometheus, Grafana |
| Cognitive Diagnostics | Ollama (Qwen 2.5) |
| Chaos Engineering | Chaos Mesh |
| Testing | Go `testing` package, Python `pytest` |
| CI/CD | GitHub Actions |

---

## Documentation

- [Architecture](./docs/architecture/README.md)
- [Observability](./docs/observability.md)
- [GitHub Workflows](./docs/workflows.md)

---

## Local Setup

Compile the Go completions proxy statically:

```bash
make install
make build-go
```

Run checks:

```bash
make test      # Runs Go and Python unit tests
make test-k3s  # Runs cluster pod end-to-end loopback validation
make lint      # Runs Go, Python, Markdown, and Kubernetes manifest linters
make fmt       # Formats all source files
```

Deploy infrastructure:

For complete bootstrap instructions, cluster configuration, and chaos engineering steps, refer to [k3s/README.md](./k3s/README.md).
