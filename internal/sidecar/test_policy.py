import asyncio
import os
from unittest.mock import AsyncMock, patch, MagicMock, mock_open
import pytest
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


@pytest.mark.parametrize(
    "input_data, expected, want_err",
    [
        pytest.param(
            "My US SSN is 123-45-6789",
            "My US SSN is [REDACTED_SSN]",
            False,
            id="us_ssn_masking",
        ),
        pytest.param(
            "Canadian SIN with spaces 123 456 789",
            "Canadian SIN with spaces [REDACTED_SIN]",
            False,
            id="canadian_sin_spaces_masking",
        ),
        pytest.param(
            "Canadian SIN with hyphens 123-456-789",
            "Canadian SIN with hyphens [REDACTED_SIN]",
            False,
            id="canadian_sin_hyphens_masking",
        ),
        pytest.param(
            "Credit Card is 1234-5678-1234-5678",
            "Credit Card is [REDACTED_CC]",
            False,
            id="credit_card_hyphens_masking",
        ),
        pytest.param(
            "No PII in this text prompt",
            "No PII in this text prompt",
            False,
            id="no_pii_clean_payload",
        ),
        pytest.param(
            "Mixed SSN 123-45-6789 and CC 1111 2222 3333 4444",
            "Mixed SSN [REDACTED_SSN] and CC [REDACTED_CC]",
            False,
            id="mixed_pii_payload",
        ),
    ],
)
def test_mask_text(input_data, expected, want_err):
    if want_err:
        with pytest.raises(Exception):
            mask_text(input_data)
    else:
        assert mask_text(input_data) == expected


# ==============================================================================
# LIFECYCLE & DECORATOR TESTS
# ==============================================================================


def test_uds_lifecycle_handler_success():
    """Verify that uds_lifecycle_handler executes the handler and closes the connection cleanly."""
    mock_reader = AsyncMock()
    mock_writer = AsyncMock()
    mock_writer.close = MagicMock()
    called = []

    @uds_lifecycle_handler
    async def dummy_dummy_handler(reader, writer):
        called.append((reader, writer))

    asyncio.run(dummy_dummy_handler(mock_reader, mock_writer))

    assert called == [(mock_reader, mock_writer)]
    mock_writer.close.assert_called_once()
    mock_writer.wait_closed.assert_awaited_once()


def test_uds_lifecycle_handler_exception():
    """Verify that uds_lifecycle_handler traps exceptions inside the handler and ensures cleanup."""
    mock_reader = AsyncMock()
    mock_writer = AsyncMock()
    mock_writer.close = MagicMock()

    @uds_lifecycle_handler
    async def dummy_dummy_handler(reader, writer):
        raise ValueError("Simulated handler crash")

    # Exception must not propagate (trapped by decorator)
    asyncio.run(dummy_dummy_handler(mock_reader, mock_writer))

    mock_writer.close.assert_called_once()
    mock_writer.wait_closed.assert_awaited_once()


@patch("os.path.exists")
@patch("os.path.dirname")
@patch("os.makedirs")
@patch("os.unlink")
def test_uds_server_lifecycle_clean_setup_and_teardown(
    mock_unlink, mock_makedirs, mock_dirname, mock_exists
):
    """Verify that uds_server_lifecycle handles directory verification, pre-unlinking, and teardown cleanup."""
    mock_exists.return_value = True
    mock_dirname.return_value = "/tmp/shared"
    called = []

    @uds_server_lifecycle("/tmp/shared/test.sock")
    async def dummy_dummy_main():
        called.append(True)

    asyncio.run(dummy_dummy_main())

    assert called == [True]
    # Check that it unlinked twice: once during setup/startup and once in finally block
    assert mock_unlink.call_count == 2
    mock_unlink.assert_any_call("/tmp/shared/test.sock")


@patch("os.path.exists")
@patch("os.path.dirname")
@patch("os.makedirs")
@patch("os.unlink")
def test_uds_server_lifecycle_cancelled_error(
    mock_unlink, mock_makedirs, mock_dirname, mock_exists
):
    """Verify that uds_server_lifecycle traps CancelledError and still executes the cleanup finally block."""
    mock_exists.return_value = True
    mock_dirname.return_value = "/tmp/shared"

    @uds_server_lifecycle("/tmp/shared/test.sock")
    async def dummy_dummy_main():
        raise asyncio.CancelledError()

    # CancelledError must not propagate (trapped by decorator)
    asyncio.run(dummy_dummy_main())

    # Teardown unlinking in finally block must still be executed
    assert mock_unlink.call_count == 2
    mock_unlink.assert_any_call("/tmp/shared/test.sock")


# ==============================================================================
# TELEMETRY & AIOPS EVALUATION TESTS
# ==============================================================================


def test_get_cpu_utilization():
    policy.last_cpu_usage = None
    policy.last_cpu_time = None
    os.environ["LIMITS_CPU"] = "1"

    # First call sets the baseline
    with patch("builtins.open", mock_open(read_data="usage_usec 10000\n")):
        with patch("time.time", side_effect=[1000.0, 1001.0]):
            util1 = get_cpu_utilization()
            assert util1 is None

    # Second call computes delta: usage delta = 10,000,000 ns, time delta = 1.0s (1e9 ns).
    # cpu util = (10,000,000 / 1e9) * 100.0 / 1.0 (limit_cores = 1.0) = 1.0%
    with patch("builtins.open", mock_open(read_data="usage_usec 20000\n")):
        with patch("time.time", side_effect=[1001.0, 1002.0]):
            util2 = get_cpu_utilization()
            assert util2 is not None
            assert abs(util2 - 1.0) < 0.01


def test_get_memory_utilization():
    with patch("builtins.open", mock_open(read_data="1048576\n")):
        util = get_memory_utilization()
        # memory limit default is 512Mi = 536870912 bytes
        # 1048576 / 536870912 * 100 = ~0.195%
        assert abs(util - 0.1953) < 0.001


def test_get_proxy_metrics():
    metrics_text = (
        "gen_ai_usage_input_tokens 5\n"
        "gen_ai_usage_output_tokens 10\n"
        "gen_ai_client_request_duration_histogram_sum 0.5\n"
        "gen_ai_client_request_duration_histogram_count 2\n"
    )
    stats = get_proxy_metrics(metrics_text)
    assert stats["input_tokens"] == 5
    assert stats["output_tokens"] == 10
    assert stats["avg_duration_seconds"] == 0.25
    assert stats["request_count"] == 2


@patch("os.path.exists", return_value=True)
def test_parse_logs(mock_exists):
    policy.log_file_offset = 0

    log_content = '{"status": 200, "duration_seconds": 0.05, "msg": "ok"}\n'
    with patch("builtins.open", mock_open(read_data=log_content)):
        logs = parse_logs()
        assert len(logs) == 1
        assert logs[0]["status"] == 200


def test_detect_anomalies():
    logs = [
        {"status": 500, "msg": "Internal Server Error"},
        {"status": 200, "duration_seconds": 0.25},
        {"msg": "PII policy engine unreachable"},
    ]
    anomalies = detect_anomalies(
        85.0, 90.0, logs, health_ok=False, ready_ok=False, network_latency=0.3
    )

    assert any("High Pod CPU" in a for a in anomalies)
    assert any("High Pod Memory" in a for a in anomalies)
    assert any("healthz check failed" in a for a in anomalies)
    assert any("readyz check failed" in a for a in anomalies)
    assert any("server error status: 500" in a for a in anomalies)
    assert any("High request latency" in a for a in anomalies)
    assert any("High outbound network latency" in a for a in anomalies)
    assert any(
        "Go Proxy reported: PII policy engine unreachable" in a for a in anomalies
    )


def test_build_prompt_context():
    stats = {
        "request_count": 5,
        "input_tokens": 10,
        "output_tokens": 20,
        "avg_duration_seconds": 0.05,
    }
    anomalies = ["High Pod CPU Utilization: 85.00%"]
    ctx = build_prompt_context(85.0, 50.0, stats, anomalies)

    assert "=== Telemetry Diagnostics Context ===" in ctx
    assert "System Status: ANOMALOUS" in ctx
    assert "Pod CPU Utilization: 85.0%" in ctx
    assert (
        "[ALERT] High Pod Pod CPU Utilization" in ctx or "[ALERT] High Pod CPU" in ctx
    )


@patch("urllib.request.urlopen")
def test_query_ollama(mock_urlopen):
    mock_response = MagicMock()
    mock_response.read.return_value = (
        b'{"response": "[RCA] Simulated Root Cause Analysis result."}'
    )
    mock_urlopen.return_value.__enter__.return_value = mock_response

    res = asyncio.run(query_ollama("test prompt"))
    assert res == "[RCA] Simulated Root Cause Analysis result."


def test_container_restart_detection():
    policy.last_restart_time = None

    # Verify check_container_restart on clean boot
    with patch("os.path.exists", return_value=False):
        with patch("builtins.open", mock_open()):
            policy.check_container_restart()
            assert policy.last_restart_time is None

    # Verify check_container_restart on crash recovery
    with patch("os.path.exists", return_value=True):
        with patch("builtins.open", mock_open(read_data="12345.0")):
            policy.check_container_restart()
            assert policy.last_restart_time is not None

    # Verify detect_anomalies registers the anomaly
    anomalies = policy.detect_anomalies(
        cpu_util=0.0, mem_util=0.0, logs=[], health_ok=True, ready_ok=True
    )
    assert any("Sidecar container restart detected" in a for a in anomalies)
