import asyncio
from unittest.mock import AsyncMock, patch, MagicMock, mock_open
import pytest
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
    # Reset states for clean test
    import internal.sidecar.policy as policy
    policy.last_cpu_idle = None
    policy.last_cpu_total = None

    metrics_text_1 = (
        "node_cpu_seconds_total{mode=\"idle\"} 10.0\n"
        "node_cpu_seconds_total{mode=\"user\"} 10.0\n"
    )
    metrics_text_2 = (
        "node_cpu_seconds_total{mode=\"idle\"} 15.0\n"
        "node_cpu_seconds_total{mode=\"user\"} 25.0\n"
    )
    # First call sets the baseline
    util1 = get_cpu_utilization(metrics_text_1)
    assert util1 is None

    # Second call computes delta: idle delta = 5, total delta = 15.
    # cpu util = (1 - 5/15) * 100 = 66.67%
    util2 = get_cpu_utilization(metrics_text_2)
    assert util2 is not None
    assert abs(util2 - 75.0) < 0.01


def test_get_memory_utilization():
    metrics_text = (
        "node_memory_MemTotal_bytes 1000\n"
        "node_memory_MemAvailable_bytes 200\n"
    )
    util = get_memory_utilization(metrics_text)
    assert util == 80.0


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
    import internal.sidecar.policy as policy
    policy.log_file_offset = 0

    log_content = '{"status": 200, "duration_seconds": 0.05, "msg": "ok"}\n'
    with patch("builtins.open", mock_open(read_data=log_content)):
        logs = parse_logs()
        assert len(logs) == 1
        assert logs[0]["status"] == 200


def test_detect_anomalies():
    logs = [{"status": 500, "msg": "Internal Server Error"}, {"status": 200, "duration_seconds": 0.25}]
    anomalies = detect_anomalies(85.0, 90.0, logs, health_ok=False, ready_ok=False)

    assert any("High Host CPU" in a for a in anomalies)
    assert any("High Host Memory" in a for a in anomalies)
    assert any("healthz check failed" in a for a in anomalies)
    assert any("readyz check failed" in a for a in anomalies)
    assert any("server error status: 500" in a for a in anomalies)
    assert any("High request latency" in a for a in anomalies)


def test_build_prompt_context():
    stats = {
        "request_count": 5,
        "input_tokens": 10,
        "output_tokens": 20,
        "avg_duration_seconds": 0.05
    }
    anomalies = ["High Host CPU Utilization: 85.00%"]
    ctx = build_prompt_context(85.0, 50.0, stats, anomalies)

    assert "=== Telemetry Diagnostics Context ===" in ctx
    assert "System Status: ANOMALOUS" in ctx
    assert "Host CPU Utilization: 85.0%" in ctx
    assert "[ALERT] High Host CPU Utilization: 85.00%" in ctx
