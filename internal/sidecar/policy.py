import asyncio
from functools import wraps
import json
import os
import re
import signal
import time
import urllib.request

SOCKET_PATH = "/tmp/shared/policy.sock"
LOG_PATH = "/tmp/shared/gateway.log"
DIAGNOSTICS_PATH = "/tmp/shared/diagnostics.txt"

# Pre-compile regex patterns for high-performance string matching
SSN_REGEX = re.compile(r"\b\d{3}-\d{2}-\d{4}\b")
SIN_REGEX = re.compile(r"\b\d{3}[- ]\d{3}[- ]\d{3}\b")
CC_REGEX = re.compile(r"\b\d{4}[- ]?\d{4}[- ]?\d{4}[- ]?\d{4}\b")

# In-memory states for telemetry and anomalies
last_cpu_idle = None
last_cpu_total = None
log_file_offset = 0
request_latencies = []
http_errors = 0
latest_context = ""


def uds_lifecycle_handler(func):
    """Decorator to manage connection lifecycle safety, trapping exceptions,
    and ensuring deterministic socket closing.
    """

    @wraps(func)
    async def wrapper(reader, writer):
        try:
            await func(reader, writer)
        except Exception as e:
            print(f"Error handling IPC client: {e}")
        finally:
            writer.close()
            await writer.wait_closed()

    return wrapper


def uds_server_lifecycle(path):
    """Decorator to manage server-level boot, teardown, error trapping,
    and UDS cleanup.
    """

    def decorator(func):
        @wraps(func)
        async def wrapper(*args, **kwargs):
            # Ensure directory is dynamically resolved for UDS mounts
            socket_dir = os.path.dirname(path)
            if socket_dir and not os.path.exists(socket_dir):
                os.makedirs(socket_dir, exist_ok=True)

            if os.path.exists(path):
                os.unlink(path)

            try:
                await func(*args, **kwargs)
            except (asyncio.CancelledError, KeyboardInterrupt):
                pass
            finally:
                if os.path.exists(path):
                    os.unlink(path)
                    print(f"Cleaned up socket file: {path}")

        return wrapper

    return decorator


def mask_text(text: str) -> str:
    text = SSN_REGEX.sub("[REDACTED_SSN]", text)
    text = SIN_REGEX.sub("[REDACTED_SIN]", text)
    return CC_REGEX.sub("[REDACTED_CC]", text)


@uds_lifecycle_handler
async def handle_client(reader, writer):
    data = await reader.readline()
    if not data:
        return

    # Read raw text from the socket for simple netcat validation in PR 1
    raw_text = data.decode("utf-8")
    masked_text = mask_text(raw_text)

    writer.write(masked_text.encode("utf-8"))
    await writer.drain()


def handle_shutdown(server, evaluation_task=None):
    print("\nShutdown signal received. Closing server...")
    server.close()
    if evaluation_task:
        evaluation_task.cancel()


def fetch_metrics():
    # Scrapes OTel Collector Prometheus exporter endpoint
    url = "http://otel-collector.telemetry.svc.cluster.local:8889/metrics"
    req = urllib.request.Request(url)
    with urllib.request.urlopen(req, timeout=2) as response:
        return response.read().decode("utf-8")


def get_cpu_utilization(metrics_text):
    global last_cpu_idle, last_cpu_total

    # Parse node_cpu_seconds_total
    idle_lines = [
        line
        for line in metrics_text.split("\n")
        if line.startswith("node_cpu_seconds_total") and 'mode="idle"' in line
    ]
    total_lines = [
        line
        for line in metrics_text.split("\n")
        if line.startswith("node_cpu_seconds_total")
    ]

    idle_sum = 0.0
    for line in idle_lines:
        parts = line.rsplit(None, 1)
        if len(parts) == 2:
            try:
                idle_sum += float(parts[1])
            except ValueError:
                pass

    total_sum = 0.0
    for line in total_lines:
        parts = line.rsplit(None, 1)
        if len(parts) == 2:
            try:
                total_sum += float(parts[1])
            except ValueError:
                pass

    if last_cpu_idle is not None and last_cpu_total is not None:
        delta_idle = idle_sum - last_cpu_idle
        delta_total = total_sum - last_cpu_total
        if delta_total > 0:
            cpu_util = (1.0 - (delta_idle / delta_total)) * 100.0
            last_cpu_idle = idle_sum
            last_cpu_total = total_sum
            return cpu_util

    last_cpu_idle = idle_sum
    last_cpu_total = total_sum
    return None


def get_memory_utilization(metrics_text):
    total = None
    available = None
    for line in metrics_text.split("\n"):
        if line.startswith("node_memory_MemTotal_bytes"):
            parts = line.rsplit(None, 1)
            if len(parts) == 2:
                total = float(parts[1])
        elif line.startswith("node_memory_MemAvailable_bytes"):
            parts = line.rsplit(None, 1)
            if len(parts) == 2:
                available = float(parts[1])

    if total and available:
        used = total - available
        return (used / total) * 100.0
    return None


def get_proxy_metrics(metrics_text):
    input_tokens = 0
    output_tokens = 0
    duration_sum = 0.0
    duration_count = 0

    for line in metrics_text.split("\n"):
        if "gen_ai_usage_input_tokens" in line:
            parts = line.rsplit(None, 1)
            if len(parts) == 2:
                try:
                    input_tokens = int(float(parts[1]))
                except ValueError:
                    pass
        elif "gen_ai_usage_output_tokens" in line:
            parts = line.rsplit(None, 1)
            if len(parts) == 2:
                try:
                    output_tokens = int(float(parts[1]))
                except ValueError:
                    pass
        elif "gen_ai_client_request_duration_histogram_sum" in line:
            parts = line.rsplit(None, 1)
            if len(parts) == 2:
                try:
                    duration_sum = float(parts[1])
                except ValueError:
                    pass
        elif "gen_ai_client_request_duration_histogram_count" in line:
            parts = line.rsplit(None, 1)
            if len(parts) == 2:
                try:
                    duration_count = int(float(parts[1]))
                except ValueError:
                    pass

    avg_duration = (duration_sum / duration_count) if duration_count > 0 else 0.0
    return {
        "input_tokens": input_tokens,
        "output_tokens": output_tokens,
        "avg_duration_seconds": avg_duration,
        "request_count": duration_count,
    }


def parse_logs():
    global log_file_offset
    if not os.path.exists(LOG_PATH):
        return []

    logs = []
    try:
        with open(LOG_PATH, "r") as f:
            f.seek(log_file_offset)
            lines = f.readlines()
            log_file_offset = f.tell()

            for line in lines:
                line = line.strip()
                if not line:
                    continue
                try:
                    logs.append(json.loads(line))
                except json.JSONDecodeError:
                    logs.append({"msg": line})
    except Exception as e:
        print(f"Error parsing log file: {e}")
    return logs


def check_health():
    health_ok = False
    ready_ok = False

    try:
        with urllib.request.urlopen(
            "http://localhost:8080/healthz", timeout=1
        ) as resp:
            if resp.status == 200:
                health_ok = True
    except Exception:
        pass

    try:
        with urllib.request.urlopen(
            "http://localhost:8080/readyz", timeout=1
        ) as resp:
            if resp.status == 200:
                ready_ok = True
    except Exception:
        pass

    return health_ok, ready_ok


def detect_anomalies(cpu_util, mem_util, logs, health_ok, ready_ok):
    global http_errors
    anomalies = []

    if cpu_util is not None and cpu_util > 80.0:
        anomalies.append(f"High Host CPU Utilization: {cpu_util:.2f}%")

    if mem_util is not None and mem_util > 80.0:
        anomalies.append(f"High Host Memory Utilization: {mem_util:.2f}%")

    if not health_ok:
        anomalies.append("Go Proxy healthz check failed")
    if not ready_ok:
        anomalies.append("Go Proxy readyz check failed (PII policy engine unreachable)")

    for log in logs:
        status = log.get("status")
        if status and status >= 500:
            http_errors += 1
            anomalies.append(
                f"Gateway returned server error status: {status} (msg: {log.get('msg')})"
            )

        dur = log.get("duration_seconds")
        if dur:
            request_latencies.append(dur)
            if len(request_latencies) > 100:
                request_latencies.pop(0)
            if dur > 0.2:
                anomalies.append(f"High request latency observed: {dur*1000:.1f}ms")

    return anomalies


def build_prompt_context(cpu_util, mem_util, proxy_stats, anomalies):
    lines = [
        "=== Telemetry Diagnostics Context ===",
        f"Timestamp: {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}",
        f"System Status: {'ANOMALOUS' if anomalies else 'HEALTHY'}",
        "",
        "Metrics Snapshot:",
        f"- Host CPU Utilization: {f'{cpu_util:.1f}%' if cpu_util is not None else 'N/A'}",
        f"- Host Memory Utilization: {f'{mem_util:.1f}%' if mem_util is not None else 'N/A'}",
        f"- Total Requests Processed: {proxy_stats['request_count']}",
        f"- Input Tokens: {proxy_stats['input_tokens']}",
        f"- Output Tokens: {proxy_stats['output_tokens']}",
        f"- Average Response Duration: {proxy_stats['avg_duration_seconds']*1000:.1f}ms",
    ]

    if anomalies:
        lines.append("")
        lines.append("Detected Anomalies:")
        for anomaly in sorted(list(set(anomalies))):
            lines.append(f"[ALERT] {anomaly}")

    lines.append("=====================================")
    return "\n".join(lines)


async def telemetry_evaluation_loop():
    global latest_context
    # Wait a bit on startup for metrics servers to initialize
    await asyncio.sleep(5)
    print("Telemetry evaluation loop started.")
    while True:
        try:
            metrics_text = ""
            try:
                metrics_text = fetch_metrics()
            except Exception as e:
                print(f"Warning: Failed to fetch metrics: {e}")

            cpu_util = get_cpu_utilization(metrics_text) if metrics_text else None
            mem_util = get_memory_utilization(metrics_text) if metrics_text else None
            proxy_stats = (
                get_proxy_metrics(metrics_text)
                if metrics_text
                else {
                    "input_tokens": 0,
                    "output_tokens": 0,
                    "avg_duration_seconds": 0.0,
                    "request_count": 0,
                }
            )

            logs = parse_logs()
            health_ok, ready_ok = check_health()
            anomalies = detect_anomalies(
                cpu_util, mem_util, logs, health_ok, ready_ok
            )

            latest_context = build_prompt_context(
                cpu_util, mem_util, proxy_stats, anomalies
            )

            # Write diagnostics to file
            with open(DIAGNOSTICS_PATH, "w") as f:
                f.write(latest_context)

        except asyncio.CancelledError:
            break
        except Exception as e:
            print(f"Error in telemetry evaluation: {e}")

        await asyncio.sleep(10)


@uds_server_lifecycle(SOCKET_PATH)
async def main():
    server = await asyncio.start_unix_server(handle_client, path=SOCKET_PATH)
    print(f"Python UDS server listening on {SOCKET_PATH}")

    evaluation_task = asyncio.create_task(telemetry_evaluation_loop())

    # Register signal handlers for clean termination (SIGTERM and SIGINT)
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, handle_shutdown, server, evaluation_task)

    async with server:
        try:
            await server.serve_forever()
        except asyncio.CancelledError:
            pass
