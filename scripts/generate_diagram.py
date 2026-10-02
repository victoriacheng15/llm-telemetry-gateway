# /// script
# requires-python = ">=3.10"
# dependencies = [
#     "diagrams",
# ]
# ///

import os
from diagrams import Cluster, Diagram, Edge
from diagrams.k8s.chaos import ChaosMesh
from diagrams.k8s.compute import Pod
from diagrams.onprem.ci import GithubActions
from diagrams.onprem.client import User
from diagrams.onprem.monitoring import Grafana, Prometheus
from diagrams.onprem.vcs import Git
from diagrams.programming.language import Go, Python

graph_attr = {
    "fontsize": "16",
    "bgcolor": "white",
    "pad": "0.5",
}

cluster_attr = {
    "margin": "30",
}

os.makedirs("docs/assets", exist_ok=True)


def generate_architecture():
    with Diagram(
        "LLM Telemetry Gateway Architecture",
        filename="docs/assets/architecture",
        show=False,
        direction="LR",
        graph_attr=graph_attr,
    ):
        client = User("Client")
        chaos = ChaosMesh("Chaos Mesh")

        with Cluster("Kubernetes Pod (Edge / Local)", graph_attr=cluster_attr):
            with Cluster("Gateway Container", graph_attr=cluster_attr):
                gateway = Go("Gateway Proxy\n(Port 8080)")

            with Cluster("Sidecar Container", graph_attr=cluster_attr):
                sidecar = Python("Policy Engine\n(PII Masking)")

            # UDS IPC inside the pod
            (
                gateway
                - Edge(
                    color="firebrick", style="bold", label="UDS Socket (/tmp/shared)"
                )
                - sidecar
            )

        with Cluster("Telemetry & Observability", graph_attr=cluster_attr):
            prometheus = Prometheus("Prometheus")
            grafana = Grafana("Grafana")
            prometheus >> grafana

        with Cluster("Inference Runtime", graph_attr=cluster_attr):
            ollama = Pod("Ollama LLM\n(qwen2.5:0.5b)")

        # Client completions traffic
        client >> Edge(color="darkgreen", label="POST /v1/chat/completions") >> gateway

        # Proxy to upstream LLM
        gateway >> Edge(color="blue", label="Sanitized Prompt") >> ollama

        # RCA loop
        (
            sidecar
            >> Edge(color="orange", style="dashed", label="AIOps RCA Queries")
            >> ollama
        )

        # Metrics scraping & emission
        (
            gateway
            >> Edge(color="purple", style="dotted", label="Scrapes & Metrics")
            >> prometheus
        )
        sidecar >> Edge(color="purple", style="dotted") >> prometheus

        # Chaos fault injection
        (
            chaos
            >> Edge(color="red", style="dashed", label="Inject Latency / Faults")
            >> gateway
        )
        chaos >> Edge(color="red", style="dashed") >> sidecar


def generate_telemetry_pipeline():
    with Diagram(
        "Metric Collection Pipeline",
        filename="docs/assets/telemetry_pipeline",
        show=False,
        direction="LR",
        graph_attr=graph_attr,
    ):
        with Cluster("Application Workload", graph_attr=cluster_attr):
            proxy = Go("Go Completions Proxy\n(gen_ai.* metrics)")

        with Cluster("Host Infrastructure", graph_attr=cluster_attr):
            node_exporter = Pod("Node Exporter\n(Host CPU & RAM)")

        with Cluster("Telemetry Collection", graph_attr=cluster_attr):
            otel = Pod("OpenTelemetry Collector\n(Port 4317 / 8889)")

        with Cluster("Storage & Visualization", graph_attr=cluster_attr):
            prometheus = Prometheus("Prometheus\nTSDB")
            grafana = Grafana("Grafana\nDashboards")
            prometheus >> Edge(color="orange", label="Datasource") >> grafana

        proxy >> Edge(color="blue", label="OTLP Push") >> otel
        (
            node_exporter
            >> Edge(color="purple", style="dashed", label="Scrape (9100)")
            >> otel
        )
        otel >> Edge(color="darkgreen", label="Prometheus Export") >> prometheus


def generate_ci_pipeline():
    with Diagram(
        "Continuous Integration Pipeline Architecture",
        filename="docs/assets/ci_pipeline",
        show=False,
        direction="LR",
        graph_attr=graph_attr,
    ):
        event = Git("Push / PR (main)")

        with Cluster("Job 1: File Change Detection", graph_attr=cluster_attr):
            filter_step = GithubActions("paths-filter\n(Inspect Diff)")

        with Cluster("Job 2: Unified App Runner (Single VM)", graph_attr=cluster_attr):
            with Cluster("Toolchains (Cached)", graph_attr=cluster_attr):
                go_tool = Go("setup-go\n(Cache)")
                py_tool = Python("setup-uv\n(Cache)")
            with Cluster("Unified Lint", graph_attr=cluster_attr):
                lint = GithubActions("make lint\n(Go + Py + MD)")
            with Cluster("Test Suites", graph_attr=cluster_attr):
                tests = GithubActions("make test\n(Unit & BDD)")

            [go_tool, py_tool] >> lint >> tests

        with Cluster("Job 3: Kubernetes Manifest Lint", graph_attr=cluster_attr):
            k8s_lint = Pod("kube-linter-action\n(Isolated Container)")

        event >> filter_step
        filter_step >> Edge(label="App Changes", color="darkgreen") >> go_tool
        filter_step >> Edge(label="App Changes", color="darkgreen") >> py_tool
        filter_step >> Edge(label="K3s Changes", color="blue") >> k8s_lint


if __name__ == "__main__":
    generate_architecture()
    generate_telemetry_pipeline()
    generate_ci_pipeline()
