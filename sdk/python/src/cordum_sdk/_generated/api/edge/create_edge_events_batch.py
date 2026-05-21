from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_agent_action_event_batch_request import EdgeAgentActionEventBatchRequest
from ...models.edge_agent_action_event_batch_response import EdgeAgentActionEventBatchResponse
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union
from uuid import UUID


def _get_kwargs(
    *,
    body: EdgeAgentActionEventBatchRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/events/batch",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeAgentActionEventBatchResponse]]:
    if response.status_code == 201:
        response_201 = EdgeAgentActionEventBatchResponse.from_dict(response.json())

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
    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409
    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413
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


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, EdgeAgentActionEventBatchResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeAgentActionEventBatchRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Response[Union[Any, EdgeAgentActionEventBatchResponse]]:
    """Append a batch of Edge agent action events

     Appends a fully prevalidated, ordered batch for existing Edge session/execution parents. Mixed-
    tenant or invalid items are rejected before append where possible. Large raw payloads must be stored
    separately and referenced by `artifact_ptrs`. Optional `Idempotency-Key` retries are scoped by
    tenant and endpoint: the same normalized batch replays the first 201 response without partial append
    or duplicate events, while the same key with a different normalized batch returns 409
    `idempotency_conflict`. The append and replay record commit in one Redis transaction. If the replay
    record has expired and any same logical `event_id` is already present, the API returns 409
    `idempotency_window_expired` and does not append duplicate events.

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeAgentActionEventBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeAgentActionEventBatchResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeAgentActionEventBatchRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Optional[Union[Any, EdgeAgentActionEventBatchResponse]]:
    """Append a batch of Edge agent action events

     Appends a fully prevalidated, ordered batch for existing Edge session/execution parents. Mixed-
    tenant or invalid items are rejected before append where possible. Large raw payloads must be stored
    separately and referenced by `artifact_ptrs`. Optional `Idempotency-Key` retries are scoped by
    tenant and endpoint: the same normalized batch replays the first 201 response without partial append
    or duplicate events, while the same key with a different normalized batch returns 409
    `idempotency_conflict`. The append and replay record commit in one Redis transaction. If the replay
    record has expired and any same logical `event_id` is already present, the API returns 409
    `idempotency_window_expired` and does not append duplicate events.

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeAgentActionEventBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeAgentActionEventBatchResponse]
    """

    return sync_detailed(
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeAgentActionEventBatchRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Response[Union[Any, EdgeAgentActionEventBatchResponse]]:
    """Append a batch of Edge agent action events

     Appends a fully prevalidated, ordered batch for existing Edge session/execution parents. Mixed-
    tenant or invalid items are rejected before append where possible. Large raw payloads must be stored
    separately and referenced by `artifact_ptrs`. Optional `Idempotency-Key` retries are scoped by
    tenant and endpoint: the same normalized batch replays the first 201 response without partial append
    or duplicate events, while the same key with a different normalized batch returns 409
    `idempotency_conflict`. The append and replay record commit in one Redis transaction. If the replay
    record has expired and any same logical `event_id` is already present, the API returns 409
    `idempotency_window_expired` and does not append duplicate events.

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeAgentActionEventBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeAgentActionEventBatchResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeAgentActionEventBatchRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Optional[Union[Any, EdgeAgentActionEventBatchResponse]]:
    """Append a batch of Edge agent action events

     Appends a fully prevalidated, ordered batch for existing Edge session/execution parents. Mixed-
    tenant or invalid items are rejected before append where possible. Large raw payloads must be stored
    separately and referenced by `artifact_ptrs`. Optional `Idempotency-Key` retries are scoped by
    tenant and endpoint: the same normalized batch replays the first 201 response without partial append
    or duplicate events, while the same key with a different normalized batch returns 409
    `idempotency_conflict`. The append and replay record commit in one Redis transaction. If the replay
    record has expired and any same logical `event_id` is already present, the API returns 409
    `idempotency_window_expired` and does not append duplicate events.

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (EdgeAgentActionEventBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeAgentActionEventBatchResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
            idempotency_key=idempotency_key,
        )
    ).parsed
