from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_agent_action_event_page_response import EdgeAgentActionEventPageResponse
from ...models.list_edge_execution_events_decision import ListEdgeExecutionEventsDecision
from ...types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime


def _get_kwargs(
    execution_id: str,
    *,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    kind: Union[Unset, str] = UNSET,
    decision: Union[Unset, ListEdgeExecutionEventsDecision] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["cursor"] = cursor

    params["limit"] = limit

    params["kind"] = kind

    json_decision: Union[Unset, str] = UNSET
    if not isinstance(decision, Unset):
        json_decision = decision.value

    params["decision"] = json_decision

    json_since: Union[Unset, str] = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: Union[Unset, str] = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/executions/{execution_id}/events".format(
            execution_id=execution_id,
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeAgentActionEventPageResponse]]:
    if response.status_code == 200:
        response_200 = EdgeAgentActionEventPageResponse.from_dict(response.json())

        return response_200
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
) -> Response[Union[Any, EdgeAgentActionEventPageResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    execution_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    kind: Union[Unset, str] = UNSET,
    decision: Union[Unset, ListEdgeExecutionEventsDecision] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeAgentActionEventPageResponse]]:
    """List Edge events for an execution

     Returns tenant-scoped events for one execution in ascending sequence order with cursor pagination
    and optional kind, decision, and RFC3339 time-window filters.

    Args:
        execution_id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        kind (Union[Unset, str]):
        decision (Union[Unset, ListEdgeExecutionEventsDecision]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeAgentActionEventPageResponse]]
    """

    kwargs = _get_kwargs(
        execution_id=execution_id,
        cursor=cursor,
        limit=limit,
        kind=kind,
        decision=decision,
        since=since,
        until=until,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    execution_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    kind: Union[Unset, str] = UNSET,
    decision: Union[Unset, ListEdgeExecutionEventsDecision] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeAgentActionEventPageResponse]]:
    """List Edge events for an execution

     Returns tenant-scoped events for one execution in ascending sequence order with cursor pagination
    and optional kind, decision, and RFC3339 time-window filters.

    Args:
        execution_id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        kind (Union[Unset, str]):
        decision (Union[Unset, ListEdgeExecutionEventsDecision]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeAgentActionEventPageResponse]
    """

    return sync_detailed(
        execution_id=execution_id,
        client=client,
        cursor=cursor,
        limit=limit,
        kind=kind,
        decision=decision,
        since=since,
        until=until,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    execution_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    kind: Union[Unset, str] = UNSET,
    decision: Union[Unset, ListEdgeExecutionEventsDecision] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeAgentActionEventPageResponse]]:
    """List Edge events for an execution

     Returns tenant-scoped events for one execution in ascending sequence order with cursor pagination
    and optional kind, decision, and RFC3339 time-window filters.

    Args:
        execution_id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        kind (Union[Unset, str]):
        decision (Union[Unset, ListEdgeExecutionEventsDecision]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeAgentActionEventPageResponse]]
    """

    kwargs = _get_kwargs(
        execution_id=execution_id,
        cursor=cursor,
        limit=limit,
        kind=kind,
        decision=decision,
        since=since,
        until=until,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    execution_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    kind: Union[Unset, str] = UNSET,
    decision: Union[Unset, ListEdgeExecutionEventsDecision] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeAgentActionEventPageResponse]]:
    """List Edge events for an execution

     Returns tenant-scoped events for one execution in ascending sequence order with cursor pagination
    and optional kind, decision, and RFC3339 time-window filters.

    Args:
        execution_id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        kind (Union[Unset, str]):
        decision (Union[Unset, ListEdgeExecutionEventsDecision]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeAgentActionEventPageResponse]
    """

    return (
        await asyncio_detailed(
            execution_id=execution_id,
            client=client,
            cursor=cursor,
            limit=limit,
            kind=kind,
            decision=decision,
            since=since,
            until=until,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
