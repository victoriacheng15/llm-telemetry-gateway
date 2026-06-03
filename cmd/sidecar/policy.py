import os
import sys
import asyncio
from internal.sidecar.policy import main

# Add project root to sys.path to support internal module imports
project_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))
if project_root not in sys.path:
    sys.path.insert(0, project_root)

if __name__ == "__main__":
    asyncio.run(main())
