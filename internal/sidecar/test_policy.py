import asyncio
from unittest.mock import AsyncMock, patch, MagicMock
import pytest
import internal.sidecar.policy as policy
from internal.sidecar.policy import (
    mask_text,
    uds_lifecycle_handler,
    uds_server_lifecycle,
)


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
    policy.handle_shutdown(mock_server)
    mock_server.close.assert_called_once()


@pytest.mark.parametrize(
    "dummy_param",
    [True],
)
@patch("asyncio.start_unix_server", new_callable=AsyncMock)
@patch("asyncio.get_running_loop")
def test_main(mock_get_loop, mock_start_server, dummy_param):
    mock_loop = MagicMock()
    mock_get_loop.return_value = mock_loop
    mock_server = AsyncMock()
    mock_start_server.return_value = mock_server

    mock_server.serve_forever.side_effect = asyncio.CancelledError()

    asyncio.run(policy.main())

    mock_start_server.assert_called_once()
    mock_loop.add_signal_handler.assert_called()
