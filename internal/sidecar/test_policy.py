import asyncio
import os
from unittest.mock import AsyncMock, patch, MagicMock, mock_open
import pytest
import multiprocessing
import internal.sidecar.policy as policy
from internal.sidecar.policy import (
    mask_text,
    uds_lifecycle_handler,
    uds_server_lifecycle,
    get_cpu_utilization,
    get_memory_utilization,
    get_proxy_metrics,
    parse_logs,
    detect_anomalies,
    build_prompt_context,
    query_ollama,
)

# ==============================================================================
# UNIFIED TABLE-DRIVEN PARAMETRIZED TESTS (IDIOMATIC PYTEST PATTERN)
# ==============================================================================


@pytest.mark.parametrize(
    "input_data, expected",
    [
        ("My US SSN is 123-45-6789", "My US SSN is [REDACTED_SSN]"),
        (
            "Canadian SIN with spaces 123 456 789",
            "Canadian SIN with spaces [REDACTED_SIN]",
        ),
        (
            "Canadian SIN with hyphens 123-456-789",
            "Canadian SIN with hyphens [REDACTED_SIN]",
        ),
        ("Credit Card is 1234-5678-1234-5678", "Credit Card is [REDACTED_CC]"),
        ("No PII in this text prompt", "No PII in this text prompt"),
        (
            "Mixed SSN 123-45-6789 and CC 1111 2222 3333 4444",
            "Mixed SSN [REDACTED_SSN] and CC [REDACTED_CC]",
        ),
    ],
)
def test_mask_text(input_data, expected):
    assert mask_text(input_data) == expected


@pytest.mark.parametrize(
    "raise_err",
    [False, True],
)
def test_uds_lifecycle_handler(raise_err):
    mock_reader = AsyncMock()
    mock_writer = AsyncMock()
    mock_writer.close = MagicMock()
    called = []

    @uds_lifecycle_handler
    async def dummy_handler(reader, writer):
        called.append(True)
        if raise_err:
            raise ValueError("Simulated handler crash")

    asyncio.run(dummy_handler(mock_reader, mock_writer))
    assert called == [True]
    mock_writer.close.assert_called_once()
    mock_writer.wait_closed.assert_awaited_once()


@pytest.mark.parametrize(
    "raise_cancel",
    [False, True],
)
def test_uds_server_lifecycle(raise_cancel):
    with patch("os.path.exists", return_value=True):
        with patch("os.path.dirname", return_value="/tmp/shared"):
            with patch("os.makedirs"):
                with patch("os.unlink") as mock_unlink:

                    @uds_server_lifecycle("/tmp/shared/test.sock")
                    async def dummy_main():
                        if raise_cancel:
                            raise asyncio.CancelledError()

                    asyncio.run(dummy_main())
                    assert mock_unlink.call_count == 2
                    mock_unlink.assert_any_call("/tmp/shared/test.sock")


@pytest.mark.parametrize(
    "limit, mem, expected_pct",
    [
        ("1G", 1073741824, 100.0),
        ("512M", 268435456, 50.0),
        ("1024K", 524288, 50.0),
        ("1000", 500, 50.0),
        ("", 268435456, 50.0),
        ("invalid", 268435456, 50.0),
    ],
)
def test_get_memory_utilization_limits(limit, mem, expected_pct):
    with patch.dict(os.environ, {"LIMITS_MEMORY": limit}):
        mock_data = f"{mem}\n"
        with patch("builtins.open", mock_open(read_data=mock_data)):
            val = get_memory_utilization()
            assert abs(val - expected_pct) < 0.01


@pytest.mark.parametrize(
    "limit, expected_cores",
    [
        ("1000m", 1.0),
        ("2", 2.0),
        ("invalid", float(multiprocessing.cpu_count())),
        ("", float(multiprocessing.cpu_count())),
    ],
)
def test_get_cpu_utilization_limits(limit, expected_cores):
    policy.last_cpu_usage = None
    policy.last_cpu_time = None

    with patch.dict(os.environ, {"LIMITS_CPU": limit}):
        with patch("builtins.open", mock_open(read_data="usage_usec 10000\n")):
            with patch("time.time", side_effect=[1000.0, 1001.0]):
                util1 = get_cpu_utilization()
                assert util1 is None

        expected_util = 1.0 / expected_cores
        with patch("builtins.open", mock_open(read_data="usage_usec 20000\n")):
            with patch("time.time", side_effect=[1001.0, 1002.0]):
                util2 = get_cpu_utilization()
                assert util2 is not None
                assert abs(util2 - expected_util) < 0.01


@pytest.mark.parametrize(
    "text, expected",
    [
        (
            "gen_ai_usage_input_tokens 5\ngen_ai_usage_output_tokens 10\ngen_ai_client_request_duration_histogram_sum 0.5\ngen_ai_client_request_duration_histogram_count 2\n",
            {
                "input_tokens": 5,
                "output_tokens": 10,
                "avg_duration_seconds": 0.25,
                "request_count": 2,
            },
        ),
        (
            "gen_ai_usage_input_tokens invalid\ngen_ai_usage_output_tokens 10\n",
            {
                "input_tokens": 0,
                "output_tokens": 10,
                "avg_duration_seconds": 0.0,
                "request_count": 0,
            },
        ),
        (
            "gen_ai_usage_output_tokens invalid\n",
            {
                "input_tokens": 0,
                "output_tokens": 0,
                "avg_duration_seconds": 0.0,
                "request_count": 0,
            },
        ),
        (
            "gen_ai_client_request_duration_histogram_sum invalid\ngen_ai_client_request_duration_histogram_count 2\n",
            {
                "input_tokens": 0,
                "output_tokens": 0,
                "avg_duration_seconds": 0.0,
                "request_count": 2,
            },
        ),
        (
            "gen_ai_client_request_duration_histogram_count invalid\n",
            {
                "input_tokens": 0,
                "output_tokens": 0,
                "avg_duration_seconds": 0.0,
                "request_count": 0,
            },
        ),
    ],
)
def test_get_proxy_metrics(text, expected):
    res = get_proxy_metrics(text)
    assert res == expected


@pytest.mark.parametrize(
    "data, expected_len, expected_msg",
    [
        ('{"status": 200, "msg": "ok"}\n', 1, "ok"),
        ("\n", 0, None),
        ("raw non-JSON log line\n", 1, "raw non-JSON log line"),
    ],
)
def test_parse_logs(data, expected_len, expected_msg):
    policy.log_file_offset = 0
    with patch("os.path.exists", return_value=True):
        with patch("builtins.open", mock_open(read_data=data)):
            logs = parse_logs()
            assert len(logs) == expected_len
            if expected_len > 0:
                assert logs[0].get("msg") == expected_msg


@pytest.mark.parametrize(
    "path_exists",
    [False],
)
def test_parse_logs_file_not_found(path_exists):
    with patch("os.path.exists", return_value=path_exists):
        logs = parse_logs()
        assert logs == []


@pytest.mark.parametrize(
    "error",
    [IOError("Permission denied")],
)
def test_parse_logs_exception(error):
    with patch("os.path.exists", return_value=True):
        with patch("builtins.open", side_effect=error):
            logs = parse_logs()
            assert logs == []


@pytest.mark.parametrize(
    "input_bytes, expected_bytes",
    [
        (b"My SSN is 123-45-6789\n", b"My SSN is [REDACTED_SSN]\n"),
        (b"", b""),
    ],
)
def test_handle_client(input_bytes, expected_bytes):
    mock_reader = AsyncMock()
    mock_reader.readline.return_value = input_bytes
    mock_writer = AsyncMock()
    mock_writer.close = MagicMock()
    mock_writer.write = MagicMock()

    asyncio.run(policy.handle_client(mock_reader, mock_writer))

    if expected_bytes:
        mock_writer.write.assert_called_once_with(expected_bytes)
    else:
        mock_writer.write.assert_not_called()


@pytest.mark.parametrize(
    "dummy_param",
    [True],
)
def test_handle_shutdown(dummy_param):
    mock_server = MagicMock()
    mock_task = MagicMock()
    policy.handle_shutdown(mock_server, mock_task)
    mock_server.close.assert_called_once()
    mock_task.cancel.assert_called_once()


@pytest.mark.parametrize(
    "exists_sequence",
    [[False, True, True]],
)
def test_uds_server_lifecycle_directory_creation(exists_sequence):
    with patch("os.path.dirname", return_value="/tmp/shared"):
        with patch("os.path.exists", side_effect=exists_sequence):
            with patch("os.makedirs") as mock_makedirs:
                with patch("os.unlink") as mock_unlink:

                    @uds_server_lifecycle("/tmp/shared/test.sock")
                    async def dummy_main():
                        pass

                    asyncio.run(dummy_main())
                    mock_makedirs.assert_called_once_with("/tmp/shared", exist_ok=True)
                    assert mock_unlink.call_count == 2


@pytest.mark.parametrize(
    "limit_size",
    [100],
)
def test_detect_anomalies_latency_cap(limit_size):
    policy.request_latencies = [0.1] * limit_size
    logs = [{"duration_seconds": 0.3}]
    detect_anomalies(0.0, 0.0, logs, True, True)
    assert len(policy.request_latencies) == limit_size


@pytest.mark.parametrize(
    "files, expected_usage",
    [
        (
            {
                "/sys/fs/cgroup/cpu.stat": "nr_periods 101\nnr_throttled 5\nusage_usec 50000\n",
            },
            50000 * 1000,
        ),
        (
            {
                "/sys/fs/cgroup/cpu.stat": IOError("cgroup v2 not mounted"),
                "/sys/fs/cgroup/cpuacct/cpuacct.usage": "90000000\n",
            },
            90000000,
        ),
        (
            {
                "/sys/fs/cgroup/cpu.stat": IOError("cgroup v2 not mounted"),
                "/sys/fs/cgroup/cpuacct/cpuacct.usage": IOError(
                    "cgroup v1 not mounted"
                ),
                "/proc/self/stat": "123 (python) S 1 1 1 1 1 1 1 1 1 1 50 100 1 1 1 1 1\n",
            },
            1500000000,
        ),
        (
            {
                "/sys/fs/cgroup/cpu.stat": IOError("err"),
                "/sys/fs/cgroup/cpuacct/cpuacct.usage": IOError("err"),
                "/proc/self/stat": IOError("err"),
            },
            None,
        ),
    ],
)
def test_cgroup_cpu_fallbacks(files, expected_usage):
    def create_mock_open(files_dict):
        def my_open(path, mode="r", *args, **kwargs):
            if path in files_dict:
                content_or_err = files_dict[path]
                if isinstance(content_or_err, Exception):
                    raise content_or_err
                return mock_open(read_data=content_or_err)(path, mode)
            raise FileNotFoundError(f"Mock open file not found: {path}")

        return my_open

    policy.last_cpu_usage = None
    policy.last_cpu_time = None
    with patch("builtins.open", side_effect=create_mock_open(files)):
        get_cpu_utilization()
        assert policy.last_cpu_usage == expected_usage


@pytest.mark.parametrize(
    "files, expected_bytes",
    [
        (
            {
                "/sys/fs/cgroup/memory.current": "1048576\n",
            },
            1048576,
        ),
        (
            {
                "/sys/fs/cgroup/memory.current": IOError("err"),
                "/sys/fs/cgroup/memory/memory.usage_in_bytes": "2097152\n",
            },
            2097152,
        ),
        (
            {
                "/sys/fs/cgroup/memory.current": IOError("err"),
                "/sys/fs/cgroup/memory/memory.usage_in_bytes": IOError("err"),
                "/proc/self/status": "Name:\tpython\nState:\tS\nVmRSS:\t\t    3072 kB\n",
            },
            3072 * 1024,
        ),
        (
            {
                "/sys/fs/cgroup/memory.current": IOError("err"),
                "/sys/fs/cgroup/memory/memory.usage_in_bytes": IOError("err"),
                "/proc/self/status": IOError("err"),
            },
            None,
        ),
    ],
)
def test_cgroup_memory_fallbacks(files, expected_bytes):
    def create_mock_open(files_dict):
        def my_open(path, mode="r", *args, **kwargs):
            if path in files_dict:
                content_or_err = files_dict[path]
                if isinstance(content_or_err, Exception):
                    raise content_or_err
                return mock_open(read_data=content_or_err)(path, mode)
            raise FileNotFoundError(f"Mock open file not found: {path}")

        return my_open

    with patch("builtins.open", side_effect=create_mock_open(files)):
        val = get_memory_utilization()
        if expected_bytes is None:
            assert val is None
        else:
            expected_pct = (expected_bytes / 536870912.0) * 100.0
            assert abs(val - expected_pct) < 0.001


@pytest.mark.parametrize(
    "fetch_metrics_val, cpu, mem, logs, health, ollama_res, ollama_err, fetch_err, get_cpu_err",
    [
        ("metrics_data", 10.0, 10.0, [], (True, True), None, None, None, None),
        (
            "metrics_data",
            90.0,
            10.0,
            [],
            (True, True),
            "[RCA] High CPU utilization.",
            None,
            None,
            None,
        ),
        (
            "metrics_data",
            90.0,
            10.0,
            [],
            (True, True),
            None,
            Exception("Ollama connection refused"),
            None,
            None,
        ),
        (
            None,
            10.0,
            10.0,
            [],
            (True, True),
            None,
            None,
            Exception("OTel collector down"),
            None,
        ),
        (
            "metrics_data",
            None,
            10.0,
            [],
            (True, True),
            None,
            None,
            None,
            ValueError("CPU read failed"),
        ),
    ],
)
@patch("internal.sidecar.policy.fetch_metrics")
@patch("internal.sidecar.policy.get_cpu_utilization")
@patch("internal.sidecar.policy.get_memory_utilization")
@patch("internal.sidecar.policy.parse_logs")
@patch("internal.sidecar.policy.check_health")
@patch("internal.sidecar.policy.query_ollama")
@patch("builtins.open", new_callable=mock_open)
@patch("asyncio.sleep")
def test_telemetry_evaluation_loop(
    mock_sleep,
    mock_file_open,
    mock_query_ollama,
    mock_check_health,
    mock_parse_logs,
    mock_get_mem,
    mock_get_cpu,
    mock_fetch_metrics,
    fetch_metrics_val,
    cpu,
    mem,
    logs,
    health,
    ollama_res,
    ollama_err,
    fetch_err,
    get_cpu_err,
):
    mock_sleep.side_effect = [None, asyncio.CancelledError()]

    if fetch_err:
        mock_fetch_metrics.side_effect = fetch_err
    else:
        mock_fetch_metrics.return_value = fetch_metrics_val

    if get_cpu_err:
        mock_get_cpu.side_effect = get_cpu_err
    else:
        mock_get_cpu.return_value = cpu

    mock_get_mem.return_value = mem
    mock_parse_logs.return_value = logs
    mock_check_health.return_value = health

    if ollama_err:
        mock_query_ollama.side_effect = ollama_err
    elif ollama_res:
        mock_query_ollama.return_value = ollama_res

    async def run_and_cancel():
        task = asyncio.create_task(policy.telemetry_evaluation_loop())
        try:
            await task
        except asyncio.CancelledError:
            pass

    asyncio.run(run_and_cancel())


@pytest.mark.parametrize(
    "cpu, mem, logs, health_ok, ready_ok, latency, restart_time, expected_anomalies",
    [
        (10.0, 15.0, [], True, True, 0.05, None, []),
        (85.0, 90.0, [], True, True, 0.05, None, ["High Pod CPU", "High Pod Memory"]),
        (
            10.0,
            15.0,
            [],
            False,
            False,
            0.05,
            None,
            ["healthz check failed", "readyz check failed"],
        ),
        (
            10.0,
            15.0,
            [
                {"status": 500, "msg": "Internal Server Error"},
                {"duration_seconds": 0.25, "status": 200},
            ],
            True,
            True,
            0.3,
            None,
            [
                "server error status: 500",
                "High request latency",
                "High outbound network latency",
            ],
        ),
        (
            10.0,
            15.0,
            [],
            True,
            True,
            0.05,
            1000.0,
            ["Sidecar container restart detected"],
        ),
    ],
)
def test_detect_anomalies(
    cpu, mem, logs, health_ok, ready_ok, latency, restart_time, expected_anomalies
):
    policy.last_restart_time = restart_time
    with patch("time.time", return_value=1010.0 if restart_time else 0.0):
        anomalies = detect_anomalies(cpu, mem, logs, health_ok, ready_ok, latency)
        for expected in expected_anomalies:
            assert any(expected in a for a in anomalies)


@pytest.mark.parametrize(
    "cpu, mem, stats, anomalies, expected_contains",
    [
        (
            12.5,
            30.0,
            {
                "request_count": 5,
                "input_tokens": 10,
                "output_tokens": 20,
                "avg_duration_seconds": 0.05,
            },
            [],
            ["System Status: HEALTHY", "Pod CPU Utilization: 12.5%"],
        ),
        (
            85.0,
            50.0,
            {
                "request_count": 5,
                "input_tokens": 10,
                "output_tokens": 20,
                "avg_duration_seconds": 0.05,
            },
            ["High CPU Utilization"],
            ["System Status: ANOMALOUS", "[ALERT] High CPU Utilization"],
        ),
    ],
)
def test_build_prompt_context(cpu, mem, stats, anomalies, expected_contains):
    ctx = build_prompt_context(cpu, mem, stats, anomalies)
    assert "=== Telemetry Diagnostics Context ===" in ctx
    for expected in expected_contains:
        assert expected in ctx


@pytest.mark.parametrize(
    "response_bytes, expected",
    [
        (b'{"response": "[RCA] Checked successfully."}', "[RCA] Checked successfully."),
    ],
)
@patch("urllib.request.urlopen")
def test_query_ollama(mock_urlopen, response_bytes, expected):
    mock_response = MagicMock()
    mock_response.read.return_value = response_bytes
    mock_urlopen.return_value.__enter__.return_value = mock_response

    res = asyncio.run(query_ollama("test prompt"))
    assert res == expected


@pytest.mark.parametrize(
    "response_bytes, expected",
    [
        (b'{"response": "[RCA] Blocking details."}', "[RCA] Blocking details."),
    ],
)
@patch("urllib.request.urlopen")
def test_query_ollama_blocking(mock_urlopen, response_bytes, expected):
    mock_response = MagicMock()
    mock_response.read.return_value = response_bytes
    mock_urlopen.return_value.__enter__.return_value = mock_response

    res = policy.query_ollama_blocking("prompt text")
    assert res == expected


@pytest.mark.parametrize(
    "file_exists, read_data, expected_restart",
    [
        (False, "", False),
        (True, "12345.0", True),
    ],
)
def test_container_restart_detection(file_exists, read_data, expected_restart):
    policy.last_restart_time = None
    with patch("os.path.exists", return_value=file_exists):
        with patch("builtins.open", mock_open(read_data=read_data)):
            policy.check_container_restart()
            has_restart = policy.last_restart_time is not None
            assert has_restart == expected_restart


@pytest.mark.parametrize(
    "response_bytes, expected",
    [
        (b"mocked metrics data", "mocked metrics data"),
    ],
)
@patch("urllib.request.urlopen")
def test_fetch_metrics(mock_urlopen, response_bytes, expected):
    mock_response = MagicMock()
    mock_response.read.return_value = response_bytes
    mock_urlopen.return_value.__enter__.return_value = mock_response

    res = policy.fetch_metrics()
    assert res == expected


@pytest.mark.parametrize(
    "side_effect, status, expected_health, expected_ready",
    [
        (None, 200, True, True),
        (Exception("connection refused"), None, False, False),
    ],
)
@patch("urllib.request.urlopen")
def test_check_health(
    mock_urlopen, side_effect, status, expected_health, expected_ready
):
    mock_urlopen.reset_mock()
    if side_effect:
        mock_urlopen.side_effect = side_effect
    else:
        mock_urlopen.side_effect = None
        mock_response = MagicMock()
        mock_response.status = status
        mock_urlopen.return_value.__enter__.return_value = mock_response

    health, ready = policy.check_health()
    assert health == expected_health
    assert ready == expected_ready


@pytest.mark.parametrize(
    "dummy_param",
    [True],
)
@patch("asyncio.start_unix_server", new_callable=AsyncMock)
@patch("asyncio.create_task")
@patch("asyncio.get_running_loop")
def test_main(mock_get_loop, mock_create_task, mock_start_server, dummy_param):
    mock_loop = MagicMock()
    mock_get_loop.return_value = mock_loop
    mock_server = AsyncMock()
    mock_start_server.return_value = mock_server

    mock_server.serve_forever.side_effect = asyncio.CancelledError()

    asyncio.run(policy.main())

    mock_start_server.assert_called_once()
    mock_create_task.assert_called_once()
    mock_loop.add_signal_handler.assert_called()

    # Close any unawaited coroutines passed to the mocked create_task
    for call in mock_create_task.call_args_list:
        coro = call[0][0]
        coro.close()
