# AGENTS.md

This file provides guidance to autonomous agents and developers when working with code in this repository.

## Project Overview

LLM Telemetry Gateway is a Kubernetes-native, fault-tolerant proxy and telemetry gateway designed for edge and self-hosted LLM environments (such as Ollama). It inspects, sanitizes, and evaluates inference traffic while monitoring pod system health in real time.

The repository consists of four core components:

1. **Go Completions Proxy (`cmd/gateway/`)**: HTTP server intercepting `/v1/chat/completions` traffic, managing Unix Domain Socket (UDS) communication with the sidecar, proxying sanitized prompts upstream to Ollama, and capturing request telemetry.
2. **Python PII Policy Engine Sidecar (`internal/sidecar/`)**: Async daemon communicating via UDS socket. Enforces fail-closed deterministic masking (SSN, credit cards, emails, phone numbers, API keys) before prompts leave the pod.
3. **AIOps Telemetry Evaluator & RCA Loop (`internal/gateway/system_metrics.go`)**: Scrapes cgroups (v1/v2) and `/proc` metrics to calculate CPU/memory limits, monitors request latency, and triggers local LLM-assisted Root Cause Analysis (RCA) upon anomaly detection.
4. **Web Observability Console (`internal/web/console/`)**: Real-time control plane dashboard powered by Server-Sent Events (SSE) displaying live RCA diagnostic logs, token usage, latency metrics, and resource utilization.

---

## Development Commands

### Dependency Management

```bash
make update    # Update Go dependencies and tidy go.mod / go.sum
make install   # Create Python virtual environment (.venv) and install dependencies
make freeze    # Freeze Python virtual environment dependencies to requirements.txt
```

### Quality Checks & Linting

```bash
make lint      # Run all linters (Go, Python, Markdown, K3s manifests)
make lint-go   # Run go vet on all Go packages
make lint-py   # Run ruff check on Python sidecar code
make lint-md   # Run markdownlint on all Markdown files
make lint-k3s  # Run kube-linter on Kubernetes manifests
```

### Code Formatting

```bash
make fmt       # Format all code across Go, Python, and Markdown
make fmt-go    # Format Go code with go fmt
make fmt-py    # Format Python code with ruff format
make fmt-md    # Fix Markdown formatting with markdownlint
```

### Testing & Coverage

```bash
make test      # Run all test suites (Go unit tests, Go BDD tests, Python tests)
make test-go   # Run Go unit tests
make test-bdd  # Run Go BDD E2E tests in ./e2e/...
make test-py   # Run Python sidecar unit tests with pytest
make cov       # Run all test coverage suites (Go and Python)
make cov-go    # Run Go test coverage
make cov-py    # Run Python test coverage with pytest-cov
```

### Building

```bash
make build-go        # Build static Go gateway binary (bin/gateway)
make build-showcase  # Generate showcase static site assets into dist/
```

### Kubernetes & Deployment

```bash
make deploy      # Apply all Kubernetes manifests (bootstrap, telemetry, apps)
make scale-down  # Scale down sandbox deployments to 0 replicas
make scale-up    # Scale up sandbox deployments to 1 replica
make test-k3s    # Run in-cluster pod verification and loopback tests
```

### Synthetic Traffic Simulation

```bash
# Run continuous synthetic traffic against local proxy (default: http://localhost:8080)
python3 scripts/traffic_generator.py --url http://localhost:8080/v1/chat/completions --interval 3.0
```

---

## Architecture

### Gateway & Sidecar Communication

- **Transport**: Unix Domain Socket mounted at a shared emptyDir/hostPath volume (default `/tmp/shared/policy-engine.sock`).
- **Protocol**: JSON payload with `{"prompt": "<raw_text>"}` and response `{"sanitized_prompt": "<masked_text>", "flagged": bool, "detected_entities": [...]}`.
- **Fail-Closed Design**: If the sidecar is unreachable or encounters an error, the proxy returns `503 Service Unavailable` rather than allowing unsanitized PII to reach the upstream LLM.

### Anomaly Detection & AIOps Loop

- System metrics (CPU usage, memory RSS, limits) are scraped alongside request metrics (latency, HTTP status codes).
- When a metric crosses threshold bounds (e.g. latency > 0.2s, CPU > 80%, sidecar socket dial error), an anomaly record is generated.
- The Go evaluator constructs a diagnostic prompt and queries the local Ollama instance (`qwen2.5:0.5b`) to synthesize an automated Root Cause Analysis entry.

### Key Directories

- `cmd/gateway/`: Entry point for the Go proxy binary.
- `cmd/showcase/`: Entry point for the showcase static site builder.
- `internal/gateway/`: Core proxy routing, UDS client, telemetry collection, and AIOps loop.
- `internal/sidecar/`: Python PII policy engine and socket server.
- `internal/web/console/`: HTML/CSS/JS frontend dashboard assets.
- `internal/web/showcase/`: Showcase template rendering and asset bundler.
- `e2e/`: BDD integration test features (Godog) and step implementations.
- `k3s/`: Kubernetes manifests (apps, bootstrap, ollama, telemetry).
- `scripts/`: Load generation and utility scripts.

---

## Development Workflow

1. **Before Making Changes**: Run `make lint` and `make test` to establish a clean baseline.
2. **After Making Changes**:
   - Run `make fmt` to apply formatting across all files.
   - Run `make lint` to verify code quality.
   - Run `make test` and `make cov` to verify unit and BDD tests pass.
3. **Commits**: Follow semantic commit formatting (`type(scope): subject`) kept strictly under 72 characters.

---

## Code Style Guidelines

- **Go**: Use standard `go fmt` and `go vet`. Avoid unnecessary third-party dependencies.
- **Python**: Use Python 3.10+ with type hints, structured classes, and `ruff` for linting and formatting.
- **Markdown**: Follow GitHub Flavored Markdown rules checked by `markdownlint-cli`.

---

## Common Pitfalls

1. **UDS Socket Deadlocks**: Always set connection timeouts when communicating over the Unix Domain Socket.
2. **Virtual Environment Isolation**: Always run Python commands within `.venv` (or through `make` targets) to avoid polluting global interpreters.
3. **Cgroup Metric Parsing**: Do not assume cgroups v2 is always present. Code in `system_metrics.go` falls back across cgroups v2, cgroups v1, `/proc`, and Go runtime statistics.
