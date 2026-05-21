from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_execution_page_response import EdgeExecutionPageResponse
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    session_id: Union[Unset, str] = UNSET,
    job_id: Union[Unset, str] = UNSET,
    trace_id: Union[Unset, str] = UNSET,
    workflow_run_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["session_id"] = session_id

    params["job_id"] = job_id

    params["trace_id"] = trace_id

    params["workflow_run_id"] = workflow_run_id

    params["cursor"] = cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/executions",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeExecutionPageResponse]]:
    if response.status_code == 200:
        response_200 = EdgeExecutionPageResponse.from_dict(response.json())

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
) -> Response[Union[Any, EdgeExecutionPageResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    session_id: Union[Unset, str] = UNSET,
    job_id: Union[Unset, str] = UNSET,
    trace_id: Union[Unset, str] = UNSET,
    workflow_run_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeExecutionPageResponse]]:
    """List Edge agent executions for a tenant

    Args:
        session_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        trace_id (Union[Unset, str]):
        workflow_run_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeExecutionPageResponse]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        job_id=job_id,
        trace_id=trace_id,
        workflow_run_id=workflow_run_id,
        cursor=cursor,
        limit=limit,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    session_id: Union[Unset, str] = UNSET,
    job_id: Union[Unset, str] = UNSET,
    trace_id: Union[Unset, str] = UNSET,
    workflow_run_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeExecutionPageResponse]]:
    """List Edge agent executions for a tenant

    Args:
        session_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        trace_id (Union[Unset, str]):
        workflow_run_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeExecutionPageResponse]
    """

    return sync_detailed(
        client=client,
        session_id=session_id,
        job_id=job_id,
        trace_id=trace_id,
        workflow_run_id=workflow_run_id,
        cursor=cursor,
        limit=limit,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    session_id: Union[Unset, str] = UNSET,
    job_id: Union[Unset, str] = UNSET,
    trace_id: Union[Unset, str] = UNSET,
    workflow_run_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeExecutionPageResponse]]:
    """List Edge agent executions for a tenant

    Args:
        session_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        trace_id (Union[Unset, str]):
        workflow_run_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeExecutionPageResponse]]
    """

    kwargs = _get_kwargs(
        session_id=session_id,
        job_id=job_id,
        trace_id=trace_id,
        workflow_run_id=workflow_run_id,
        cursor=cursor,
        limit=limit,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    session_id: Union[Unset, str] = UNSET,
    job_id: Union[Unset, str] = UNSET,
    trace_id: Union[Unset, str] = UNSET,
    workflow_run_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeExecutionPageResponse]]:
    """List Edge agent executions for a tenant

    Args:
        session_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        trace_id (Union[Unset, str]):
        workflow_run_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeExecutionPageResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            session_id=session_id,
            job_id=job_id,
            trace_id=trace_id,
            workflow_run_id=workflow_run_id,
            cursor=cursor,
            limit=limit,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
