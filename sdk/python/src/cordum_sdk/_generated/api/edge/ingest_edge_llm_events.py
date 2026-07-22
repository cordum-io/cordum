from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_error import EdgeError
from ...models.edge_llm_ingest_request import EdgeLLMIngestRequest
from ...models.edge_llm_ingest_response import EdgeLLMIngestResponse
from typing import cast
from typing import Dict



def _get_kwargs(
    *,
    body: EdgeLLMIngestRequest,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/llm/events",
    }

    _body = body.to_dict()


    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, EdgeError, EdgeLLMIngestResponse]]:
    if response.status_code == 200:
        response_200 = EdgeLLMIngestResponse.from_dict(response.json())



        return response_200
    if response.status_code == 201:
        response_201 = EdgeLLMIngestResponse.from_dict(response.json())



        return response_201
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404
    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413
    if response.status_code == 429:
        response_429 = EdgeError.from_dict(response.json())



        return response_429
    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, EdgeError, EdgeLLMIngestResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeLLMIngestRequest,
    x_tenant_id: str,

) -> Response[Union[Any, EdgeError, EdgeLLMIngestResponse]]:
    """ Ingest intercepted LLM chat turns from a trusted proxy

     Disabled by default. When `CORDUM_EDGE_LLM_INGEST_ENABLED` is unset (or non-truthy) the route
    returns 503 `service_unavailable` and persists nothing. When enabled, an authenticated LLM proxy
    holding `edge.llm.ingest` submits a bounded batch of intercepted model interactions
    (prompt/response/cost). The gateway redacts prompt and response content via the Edge redactor,
    classifies and records each turn as an `AgentActionEvent` with `layer=llm` and `decision=RECORDED`,
    and returns a per-event advisory decision (`record` | `redact`) plus the redacted content and
    detected secret finding types. The ALLOW/DENY policy decision is NOT made here — a proxy that must
    block a prompt pairs this call with `POST /api/v1/edge/evaluate` (layer-agnostic, already classifies
    `layer=llm`). The `source.source_id` must match the authenticated proxy principal and the referenced
    session/execution must exist in the tenant under the `llm-proxy` execution adapter. An optional
    bounded `nonce` is deduplicated against a Redis replay window scoped to `(tenant, llm-proxy)`. Raw
    provider keys, headers, cookies, and authorization values are rejected at the strict-schema decode
    boundary. All-or-nothing batch acceptance. See `docs/edge/llm-proxy-governance.md`.

    Args:
        x_tenant_id (str):
        body (EdgeLLMIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, EdgeLLMIngestResponse]]
     """


    kwargs = _get_kwargs(
        body=body,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeLLMIngestRequest,
    x_tenant_id: str,

) -> Optional[Union[Any, EdgeError, EdgeLLMIngestResponse]]:
    """ Ingest intercepted LLM chat turns from a trusted proxy

     Disabled by default. When `CORDUM_EDGE_LLM_INGEST_ENABLED` is unset (or non-truthy) the route
    returns 503 `service_unavailable` and persists nothing. When enabled, an authenticated LLM proxy
    holding `edge.llm.ingest` submits a bounded batch of intercepted model interactions
    (prompt/response/cost). The gateway redacts prompt and response content via the Edge redactor,
    classifies and records each turn as an `AgentActionEvent` with `layer=llm` and `decision=RECORDED`,
    and returns a per-event advisory decision (`record` | `redact`) plus the redacted content and
    detected secret finding types. The ALLOW/DENY policy decision is NOT made here — a proxy that must
    block a prompt pairs this call with `POST /api/v1/edge/evaluate` (layer-agnostic, already classifies
    `layer=llm`). The `source.source_id` must match the authenticated proxy principal and the referenced
    session/execution must exist in the tenant under the `llm-proxy` execution adapter. An optional
    bounded `nonce` is deduplicated against a Redis replay window scoped to `(tenant, llm-proxy)`. Raw
    provider keys, headers, cookies, and authorization values are rejected at the strict-schema decode
    boundary. All-or-nothing batch acceptance. See `docs/edge/llm-proxy-governance.md`.

    Args:
        x_tenant_id (str):
        body (EdgeLLMIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, EdgeLLMIngestResponse]
     """


    return sync_detailed(
        client=client,
body=body,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeLLMIngestRequest,
    x_tenant_id: str,

) -> Response[Union[Any, EdgeError, EdgeLLMIngestResponse]]:
    """ Ingest intercepted LLM chat turns from a trusted proxy

     Disabled by default. When `CORDUM_EDGE_LLM_INGEST_ENABLED` is unset (or non-truthy) the route
    returns 503 `service_unavailable` and persists nothing. When enabled, an authenticated LLM proxy
    holding `edge.llm.ingest` submits a bounded batch of intercepted model interactions
    (prompt/response/cost). The gateway redacts prompt and response content via the Edge redactor,
    classifies and records each turn as an `AgentActionEvent` with `layer=llm` and `decision=RECORDED`,
    and returns a per-event advisory decision (`record` | `redact`) plus the redacted content and
    detected secret finding types. The ALLOW/DENY policy decision is NOT made here — a proxy that must
    block a prompt pairs this call with `POST /api/v1/edge/evaluate` (layer-agnostic, already classifies
    `layer=llm`). The `source.source_id` must match the authenticated proxy principal and the referenced
    session/execution must exist in the tenant under the `llm-proxy` execution adapter. An optional
    bounded `nonce` is deduplicated against a Redis replay window scoped to `(tenant, llm-proxy)`. Raw
    provider keys, headers, cookies, and authorization values are rejected at the strict-schema decode
    boundary. All-or-nothing batch acceptance. See `docs/edge/llm-proxy-governance.md`.

    Args:
        x_tenant_id (str):
        body (EdgeLLMIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, EdgeLLMIngestResponse]]
     """


    kwargs = _get_kwargs(
        body=body,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeLLMIngestRequest,
    x_tenant_id: str,

) -> Optional[Union[Any, EdgeError, EdgeLLMIngestResponse]]:
    """ Ingest intercepted LLM chat turns from a trusted proxy

     Disabled by default. When `CORDUM_EDGE_LLM_INGEST_ENABLED` is unset (or non-truthy) the route
    returns 503 `service_unavailable` and persists nothing. When enabled, an authenticated LLM proxy
    holding `edge.llm.ingest` submits a bounded batch of intercepted model interactions
    (prompt/response/cost). The gateway redacts prompt and response content via the Edge redactor,
    classifies and records each turn as an `AgentActionEvent` with `layer=llm` and `decision=RECORDED`,
    and returns a per-event advisory decision (`record` | `redact`) plus the redacted content and
    detected secret finding types. The ALLOW/DENY policy decision is NOT made here — a proxy that must
    block a prompt pairs this call with `POST /api/v1/edge/evaluate` (layer-agnostic, already classifies
    `layer=llm`). The `source.source_id` must match the authenticated proxy principal and the referenced
    session/execution must exist in the tenant under the `llm-proxy` execution adapter. An optional
    bounded `nonce` is deduplicated against a Redis replay window scoped to `(tenant, llm-proxy)`. Raw
    provider keys, headers, cookies, and authorization values are rejected at the strict-schema decode
    boundary. All-or-nothing batch acceptance. See `docs/edge/llm-proxy-governance.md`.

    Args:
        x_tenant_id (str):
        body (EdgeLLMIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, EdgeLLMIngestResponse]
     """


    return (await asyncio_detailed(
        client=client,
body=body,
x_tenant_id=x_tenant_id,

    )).parsed
