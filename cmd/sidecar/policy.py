import asyncio
from functools import wraps
import os
import re
import signal

SOCKET_PATH = "/tmp/shared/policy.sock"

# Pre-compile regex patterns for high-performance string matching
SSN_REGEX = re.compile(r"\b\d{3}-\d{2}-\d{4}\b")
SIN_REGEX = re.compile(r"\b\d{3}[- ]\d{3}[- ]\d{3}\b")
CC_REGEX = re.compile(r"\b\d{4}[- ]?\d{4}[- ]?\d{4}[- ]?\d{4}\b")


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


def handle_shutdown(server):
    print("\nShutdown signal received. Closing server...")
    server.close()


@uds_server_lifecycle(SOCKET_PATH)
async def main():
    server = await asyncio.start_unix_server(handle_client, path=SOCKET_PATH)
    print(f"Python UDS server listening on {SOCKET_PATH}")

    # Register signal handlers for clean termination (SIGTERM and SIGINT)
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, handle_shutdown, server)

    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    asyncio.run(main())
