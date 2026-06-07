# AIOps Diagnostics & Anomaly Detection

To facilitate intelligent incident identification, the Python policy sidecar runs a telemetry collection and diagnostics evaluation loop.

## 1. Telemetry Ingestion Channels

The sidecar polls cluster metrics and local files every 10 seconds:

- **Host System Utilization**: Scrapes host-level CPU and memory statistics from the OpenTelemetry Collector's Prometheus exporter endpoint.
- **Service Endpoints**: Probes the Go completions proxy's `/healthz` and `/readyz` endpoints to verify processing and sidecar connection health.
- **Go Proxy Logs**: Reads and parses `/tmp/shared/gateway.log` in real-time to capture server errors (`5xx` status codes) and latency measurements.

## 2. Anomaly Classifications

An anomaly is triggered if any of the following boundaries are crossed:

- **Host CPU Utilization**: Exceeds the `5.0%` threshold.
- **Host Memory Utilization**: Exceeds the `60.0%` threshold.
- **HTTP Latency**: Gateway completion requests exceed `200ms`.
- **System Health**: Either the local `/healthz` or `/readyz` probes fail.
- **HTTP Server Error**: Any gateway request returns a status code `>= 500`.

## 3. RCA Synthesis (Ollama Integration)

Upon anomaly detection, the sidecar constructs a structured prompt context block and asynchronously queries a local, CPU-accelerated Ollama container running `qwen2.5:0.5b` in the `ollama` namespace. Ollama synthesizes the telemetry patterns and outputs natural-language Root Cause Analysis (RCA) logs to identify the active chaos scenario.
