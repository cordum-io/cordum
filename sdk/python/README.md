# cordum-sdk

Typed Python SDK for the Cordum control plane.

`cordum-sdk` is generated from the canonical OpenAPI spec and wrapped with a
small hand-tuned facade for:

- sync and async clients
- auth helpers
- retry/backoff policy
- typed error mapping
- pagination helpers
- streaming/SSE helpers

## Install

```bash
pip install cordum-sdk
```

## Sync quickstart

```python
from cordum_sdk import CordumClient

with CordumClient("https://api.cordum.example", auth="cordum_api_key", tenant_id="tenant-123") as client:
    jobs = client.jobs.list_jobs()
    for job in jobs.items:
        print(job.id, job.state)
```

## Async quickstart

```python
from cordum_sdk import AsyncCordumClient

async with AsyncCordumClient("https://api.cordum.example", auth="cordum_api_key", tenant_id="tenant-123") as client:
    jobs = await client.jobs.list_jobs()
    print(len(jobs.items))
```

## Authentication

### API key

```python
from cordum_sdk import CordumClient

client = CordumClient("https://api.cordum.example", auth="cordum_api_key", tenant_id="tenant-123")
```

### Bearer token

```python
from cordum_sdk import CordumClient
from cordum_sdk.auth import BearerTokenAuth

client = CordumClient(
    "https://api.cordum.example",
    auth=BearerTokenAuth("bearer-token"),
    tenant_id="tenant-123",
)
```

### Session login

```python
from cordum_sdk import AsyncCordumClient
from cordum_sdk.auth import SessionAuth

auth = SessionAuth("https://api.cordum.example", email="you@example.com", password="correct-password")
client = AsyncCordumClient("https://api.cordum.example", auth=auth, tenant_id="tenant-123")
```

### SAML redirect flow

```python
import httpx

from cordum_sdk import CordumClient
from cordum_sdk.auth import SamlAuth


def exchange_relay_state(relay_state: str) -> dict[str, str]:
    with httpx.Client(base_url="https://api.cordum.example") as http_client:
        response = http_client.post("/api/v1/auth/saml/acs", json={"relay_state": relay_state})
        response.raise_for_status()
        return response.json()


auth = SamlAuth("https://idp.example.com/sso/start", exchange_relay_state)
print("Open:", auth.initiate())
auth.complete("relay-state-from-callback")

client = CordumClient("https://api.cordum.example", auth=auth, tenant_id="tenant-123")
```

## Retries + Idempotency-Key

Retries are enabled by default for idempotent methods (`GET`, `HEAD`, `PUT`,
`DELETE`, `OPTIONS`) and for `POST` only when you supply an idempotency key.

```python
from uuid import uuid4

from cordum_sdk import CordumClient
from cordum_sdk._generated.models.submit_job_request import SubmitJobRequest

with CordumClient("https://api.cordum.example", auth="cordum_api_key", tenant_id="tenant-123") as client:
    response = client.jobs.submit_job(
        body=SubmitJobRequest(prompt="hello"),
        idempotency_key=uuid4(),
    )
    print(response.job_id)
```

## Pagination + Streaming

```python
from cordum_sdk import CordumClient

with CordumClient("https://api.cordum.example", auth="cordum_api_key", tenant_id="tenant-123") as client:
    for job in client.jobs.paginate("list_jobs"):
        print(job.id)

    for event in client.mcp.stream():
        print(event.event, event.data)
        break
```

## Error handling

| Exception | Status |
| --- | --- |
| `AuthenticationError` | 401 |
| `AuthorizationError` | 403 |
| `NotFoundError` | 404 |
| `ConflictError` | 409 |
| `ValidationError` | 400 / 422 |
| `RateLimitError` | 429 |
| `ServerError` | 5xx |
| `NetworkError` / `TimeoutError` | transport / timeout |

```python
from cordum_sdk.errors import NotFoundError
```

## Typed models

- Streaming events use the Pydantic v2 `StreamEvent` model.
- Generated request/response models live in `cordum_sdk._generated.models`.
- Friendly resource namespaces (`client.jobs`, `client.workflows`, etc.) return
  those generated typed models directly.

## Compatibility

- Python 3.10+
- Generated from `docs/api/openapi/cordum-api.yaml`
- Current bundled spec version: `2026-05-09.2`

## Examples

- `examples/hello_world.py`
- `examples/async_streaming.py`
- `docs/quickstart.md`

## Regenerating from the spec

The `generator` extra pins the toolchain: openapi-python-client 0.21.7,
click 8.1.8, typer 0.12.5, pydantic 2.9.2, Jinja2 3.1.6 and ruff 0.7.4. Use
Python 3.12; 3.11 and 3.13 give the same output. Python 3.10 orders some
imports differently, and pydantic 2.9.2 has no Python 3.14 wheel.

```bash
python3.12 -m venv .venv
.venv/bin/python -m pip install -e ".[generator]"
bash scripts/generate.sh
```

On Windows PowerShell:

```powershell
py -3.12 -m venv .venv
.venv\Scripts\python -m pip install -e ".[generator]"
.\scripts\generate.ps1
```

The scripts prefer `.venv`, set `PYTHONHASHSEED=0` and run `ruff format` with
LF line endings, so two runs give byte-identical output.
