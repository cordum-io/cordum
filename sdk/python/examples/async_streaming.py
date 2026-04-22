from __future__ import annotations

import asyncio
import os

from cordum_sdk import AsyncCordumClient

BASE_URL = os.environ.get("CORDUM_BASE_URL", "http://localhost:8080")
API_KEY = os.environ.get("CORDUM_API_KEY", "dev-api-key")
TENANT_ID = os.environ.get("CORDUM_TENANT_ID", "tenant-dev")


async def amain() -> None:
    async with AsyncCordumClient(BASE_URL, auth=API_KEY, tenant_id=TENANT_ID) as client:
        async for event in client.mcp.stream():
            print("event={event} data={data}".format(event=event.event, data=event.data))
            break


if __name__ == "__main__":
    asyncio.run(amain())
