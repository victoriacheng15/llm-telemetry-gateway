import asyncio
import os
import re

SOCKET_PATH = "/tmp/shared/policy.sock"

# Pre-compile regex patterns for high-performance string matching
SSN_REGEX = re.compile(r"\b\d{3}-\d{2}-\d{4}\b")
SIN_REGEX = re.compile(r"\b\d{3}[- ]\d{3}[- ]\d{3}\b")
CC_REGEX = re.compile(r"\b\d{4}[- ]?\d{4}[- ]?\d{4}[- ]?\d{4}\b")

def mask_text(text: str) -> str:
    text = SSN_REGEX.sub("[REDACTED_SSN]", text)
    text = SIN_REGEX.sub("[REDACTED_SIN]", text)
    return CC_REGEX.sub("[REDACTED_CC]", text)

async def handle_client(reader, writer):
    try:
        data = await reader.readline()
        if not data:
            return
        
        # Read raw text from the socket for simple netcat validation in PR 1
        raw_text = data.decode("utf-8")
        masked_text = mask_text(raw_text)
        
        writer.write(masked_text.encode("utf-8"))
        await writer.drain()
    except Exception as e:
        print(f"Error handling IPC client: {e}")
    finally:
        writer.close()
        await writer.wait_closed()

async def main():
    # Ensure directory is dynamically resolved for UDS mounts
    socket_dir = os.path.dirname(SOCKET_PATH)
    if socket_dir and not os.path.exists(socket_dir):
        os.makedirs(socket_dir, exist_ok=True)
        
    if os.path.exists(SOCKET_PATH):
        os.unlink(SOCKET_PATH)
        
    server = await asyncio.start_unix_server(handle_client, path=SOCKET_PATH)
    print(f"Python UDS server listening on {SOCKET_PATH}")
    
    async with server:
        await server.serve_forever()

if __name__ == "__main__":
    asyncio.run(main())
