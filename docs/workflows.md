# Platform Workflows

This document details the CI/CD and automation paths that validate the LLM Telemetry Gateway.

---

## 📂 Core Workflows

### 🚢 [Continuous Integration](../.github/workflows/ci.yml)

The central pipeline that coordinates the parallel validation and linting of the gateway applications.

- **Trigger**: Push or Pull Request targeting the main branch.
- **Responsibility**: Detects modified files and triggers Go, Python, or Kubernetes checks conditionally.
- **Key Feature**: Leverages path-filtering to execute only the jobs corresponding to the modified components.

### 🧪 [Go Lint & Test](../.github/workflows/ci.yml)

Ensures code quality and functional correctness across Go completions proxy packages.

- **Trigger**: File changes detected in the Go codebase.
- **Responsibility**: Validates syntax via `go vet` and executes the full suite of table-driven tests.
- **Key Feature**: Centralized cache management to speed up mod download times on runner setup.

### 🐍 [Python Lint & Test](../.github/workflows/ci.yml)

Validates the asynchronous PII masking policy engine sidecar.

- **Trigger**: File changes detected in the Python codebase.
- **Responsibility**: Checks formatting and style conventions using Ruff, and executes unit tests.
- **Key Feature**: Verifies socket unlinking and signal handlers to ensure clean teardown behavior.

### 🏗️ [Kubernetes Linting](../.github/workflows/ci.yml)

Validates the Kubernetes configurations and resource limits inside the cluster directory.

- **Trigger**: File changes detected in the K3s manifests.
- **Responsibility**: Scans resource definitions to enforce best-practice limits and namespace bindings.
- **Key Feature**: Automated static analysis via `kube-linter` to catch configuration bugs before deployment.

### 📝 [Markdown Linting](../.github/workflows/ci.yml)

Enforces syntax and format consistency across all Markdown documentation files.

- **Trigger**: File changes detected in Markdown files.
- **Responsibility**: Scans documentation directories using `markdownlint-cli` to enforce formatting rules.
- **Key Feature**: Automated layout verification protecting project operational memory documents.
