import asyncio
from unittest.mock import AsyncMock, patch, MagicMock
import pytest
from internal.sidecar.policy import (
    mask_text,
    uds_lifecycle_handler,
    uds_server_lifecycle,
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
