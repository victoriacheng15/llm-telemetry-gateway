import asyncio
from functools import wraps
import json
import multiprocessing
import os
import re
import signal
import time
import urllib.request

SOCKET_PATH = "/tmp/shared/policy.sock"
LOG_PATH = "/tmp/shared/gateway.log"
DIAGNOSTICS_PATH = "/tmp/shared/diagnostics.txt"
RCA_LOG_PATH = "/tmp/shared/rca.log"

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
LAST_SEEN_PATH = "/tmp/shared/sidecar_last_seen"
last_restart_time = None


def check_container_restart():
    global last_restart_time
    if os.path.exists(LAST_SEEN_PATH):
        try:
            with open(LAST_SEEN_PATH, "r") as f:
                last_seen = float(f.read().strip())
            last_restart_time = time.time()
            print(f"Container restart detected! Last seen: {last_seen}")
        except Exception:
            pass
    update_last_seen()


def update_last_seen():
    try:
        with open(LAST_SEEN_PATH, "w") as f:
            f.write(str(time.time()))
    except Exception:
        pass


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


last_cpu_usage = None
last_cpu_time = None


def get_cpu_utilization():
    global last_cpu_usage, last_cpu_time

    cpu_ns = None
    try:
        with open("/sys/fs/cgroup/cpu.stat", "r") as f:
            for line in f:
                if line.startswith("usage_usec"):
                    cpu_ns = int(line.split()[1]) * 1000
    except Exception:
        pass
    if cpu_ns is None:
        try:
            with open("/sys/fs/cgroup/cpuacct/cpuacct.usage", "r") as f:
                cpu_ns = int(f.read().strip())
        except Exception:
            pass
    if cpu_ns is None:
        try:
            with open("/proc/self/stat", "r") as f:
                parts = f.read().split(")")
                fields = parts[1].split()
                utime = int(fields[11])
                stime = int(fields[12])
                cpu_ns = (utime + stime) * 10000000
        except Exception:
            return None

    now = time.time()
    cpu_util = None
    if last_cpu_usage is not None and last_cpu_time is not None:
        delta_ns = cpu_ns - last_cpu_usage
        delta_time = (now - last_cpu_time) * 1e9
        if delta_time > 0 and delta_ns >= 0:
            limit_str = os.getenv("LIMITS_CPU", "")
            limit_cores = 1.0
            if "m" in limit_str:
                try:
                    limit_cores = float(limit_str.replace("m", "")) / 1000.0
                except Exception:
                    limit_cores = float(multiprocessing.cpu_count())
            elif limit_str:
                try:
                    limit_cores = float(limit_str)
                except Exception:
                    limit_cores = float(multiprocessing.cpu_count())
            else:
                limit_cores = float(multiprocessing.cpu_count())

            cpu_util = (delta_ns / delta_time) * 100.0 / limit_cores
            if cpu_util > 100.0:
                cpu_util = 100.0

    last_cpu_usage = cpu_ns
    last_cpu_time = now
    return cpu_util


def get_memory_utilization():
    mem_bytes = None
    try:
        with open("/sys/fs/cgroup/memory.current", "r") as f:
            mem_bytes = int(f.read().strip())
    except Exception:
        pass
    if mem_bytes is None:
        try:
            with open("/sys/fs/cgroup/memory/memory.usage_in_bytes", "r") as f:
                mem_bytes = int(f.read().strip())
        except Exception:
            pass
    if mem_bytes is None:
        try:
            with open("/proc/self/status", "r") as f:
                for line in f:
                    if line.startswith("VmRSS:"):
                        mem_bytes = int(line.split()[1]) * 1024
        except Exception:
            return None

    limit_str = os.getenv("LIMITS_MEMORY", "")
    limit_bytes = 512.0 * 1024 * 1024
    if limit_str:
        match = re.match(
            r"^(\d+(?:\.\d+)?)\s*([KMGT]i?B?|B)?$", limit_str, re.IGNORECASE
        )
        if match:
            val = float(match.group(1))
            unit = (match.group(2) or "").upper()
            match unit[:1]:
                case "G":
                    limit_bytes = val * 1024 * 1024 * 1024
                case "M":
                    limit_bytes = val * 1024 * 1024
                case "K":
                    limit_bytes = val * 1024
                case _:
                    limit_bytes = val

    return (mem_bytes / limit_bytes) * 100.0


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
        with urllib.request.urlopen("http://localhost:8080/healthz", timeout=1) as resp:
            if resp.status == 200:
                health_ok = True
    except Exception:
        pass

    try:
        with urllib.request.urlopen("http://localhost:8080/readyz", timeout=1) as resp:
            if resp.status == 200:
                ready_ok = True
    except Exception:
        pass

    return health_ok, ready_ok


def detect_anomalies(
    cpu_util, mem_util, logs, health_ok, ready_ok, network_latency=0.0
):
    global last_restart_time, http_errors
    anomalies = []

    if last_restart_time is not None and (time.time() - last_restart_time) < 30.0:
        anomalies.append(
            "Sidecar container restart detected (potential process crash or kill)"
        )

    if cpu_util is not None and cpu_util > 80.0:
        anomalies.append(f"High Pod CPU Utilization: {cpu_util:.2f}%")

    if mem_util is not None and mem_util > 80.0:
        anomalies.append(f"High Pod Memory Utilization: {mem_util:.2f}%")

    if network_latency > 0.2:
        anomalies.append(
            f"High outbound network latency: {network_latency * 1000:.1f}ms"
        )

    if not health_ok:
        anomalies.append("Go Proxy healthz check failed")
    if not ready_ok:
        anomalies.append("Go Proxy readyz check failed (PII policy engine unreachable)")

    for log in logs:
        msg = log.get("msg", "")
        if "PII policy engine unreachable" in msg or "Readiness check failed" in msg:
            anomalies.append(f"Go Proxy reported: {msg}")

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
                anomalies.append(f"High request latency observed: {dur * 1000:.1f}ms")

    return anomalies


def build_prompt_context(cpu_util, mem_util, proxy_stats, anomalies):
    lines = [
        "=== Telemetry Diagnostics Context ===",
        f"Timestamp: {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}",
        f"System Status: {'ANOMALOUS' if anomalies else 'HEALTHY'}",
        "",
        "Metrics Snapshot:",
        f"- Pod CPU Utilization: {f'{cpu_util:.1f}%' if cpu_util is not None else 'N/A'}",
        f"- Pod Memory Utilization: {f'{mem_util:.1f}%' if mem_util is not None else 'N/A'}",
        f"- Total Requests Processed: {proxy_stats['request_count']}",
        f"- Input Tokens: {proxy_stats['input_tokens']}",
        f"- Output Tokens: {proxy_stats['output_tokens']}",
        f"- Average Response Duration: {proxy_stats['avg_duration_seconds'] * 1000:.1f}ms",
    ]

    if anomalies:
        lines.append("")
        lines.append("Detected Anomalies:")
        for anomaly in sorted(list(set(anomalies))):
            lines.append(f"[ALERT] {anomaly}")

    lines.append("=====================================")
    return "\n".join(lines)


def query_ollama_blocking(prompt):
    url = "http://ollama.ollama.svc.cluster.local:11434/api/generate"
    data = json.dumps(
        {"model": "qwen2.5:0.5b", "prompt": prompt, "stream": False}
    ).encode("utf-8")

    req = urllib.request.Request(
        url, data=data, headers={"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        res_data = json.loads(resp.read().decode("utf-8"))
        return res_data.get("response", "")


async def query_ollama(prompt):
    return await asyncio.to_thread(query_ollama_blocking, prompt)


async def telemetry_evaluation_loop():
    global latest_context
    # Wait a bit on startup for metrics servers to initialize
    await asyncio.sleep(5)
    print("Telemetry evaluation loop started.")
    check_container_restart()
    while True:
        try:
            update_last_seen()
            metrics_text = ""
            metrics_latency = 0.0
            try:
                start_t = time.time()
                metrics_text = fetch_metrics()
                metrics_latency = time.time() - start_t
            except Exception as e:
                print(f"Warning: Failed to fetch metrics: {e}")

            cpu_util = get_cpu_utilization()
            mem_util = get_memory_utilization()
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
                cpu_util, mem_util, logs, health_ok, ready_ok, metrics_latency
            )

            latest_context = build_prompt_context(
                cpu_util, mem_util, proxy_stats, anomalies
            )

            # Write diagnostics to file
            with open(DIAGNOSTICS_PATH, "w") as f:
                f.write(latest_context)

            # RCA Diagnosis using Ollama
            if anomalies:
                prompt = (
                    "You are an AIOps diagnostic agent. Analyze the system telemetry context below and perform a "
                    "Root Cause Analysis (RCA) to identify which active chaos scenario is happening.\n"
                    "Choose exactly one of the following scenarios:\n"
                    '1. "Network Delay" (high request duration/latency)\n'
                    '2. "Sidecar Process Crash" (healthz/readyz check failing, connection refused)\n'
                    '3. "Resource Starvation / Stress" (high pod CPU or memory utilization)\n'
                    '4. "Healthy / Nominal" (no anomalies)\n\n'
                    "System Telemetry Context:\n"
                    f"{latest_context}\n\n"
                    "Provide a short, direct natural-language Root Cause Analysis log (max 2 sentences) identifying "
                    "the active scenario and the primary symptom. Start with '[RCA] '."
                )
                try:
                    rca_response = await query_ollama(prompt)
                    rca_log = f"[{time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}] {rca_response.strip()}"
                    print(f"[RCA DIAGNOSTICS] {rca_log}")
                    with open(RCA_LOG_PATH, "a") as f:
                        f.write(rca_log + "\n")
                except Exception as e:
                    print(f"Warning: Failed to query Ollama: {e}")
            else:
                rca_log = f"[{time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}] INFO: Nominal system health. No anomalies detected."
                print(f"[RCA DIAGNOSTICS] {rca_log}")
                with open(RCA_LOG_PATH, "a") as f:
                    f.write(rca_log + "\n")

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
