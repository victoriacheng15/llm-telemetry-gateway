# Architecture Documentation

This directory outlines the detailed blueprints, IPC mechanisms, reliability models, and diagnostics logic of the LLM Telemetry Gateway.

## 🏗️ Architecture Modules

- **[Core Architecture & IPC](./core.md)**: Conceptual overview, request routing, Unix Domain Socket configuration, and reliability safety models.
- **[AIOps Diagnostics & Anomaly Detection](./aiops.md)**: Telemetry scraping, metrics thresholds, and Ollama RCA engine integrations.
- **[Triggering AIOps Diagnostics](./trigger.md)**: Steps to inject chaos, monitor anomaly thresholds, and view generated LLM RCA reports.
