from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.submit_job_request import SubmitJobRequest
from ...models.submit_job_response import SubmitJobResponse
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union
from uuid import UUID


def _get_kwargs(
    *,
    body: SubmitJobRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/jobs",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, SubmitJobResponse]]:
    if response.status_code == 200:
        response_200 = SubmitJobResponse.from_dict(response.json())

        return response_200
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413
    if response.status_code == 429:
        response_429 = cast(Any, None)
        return response_429
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, SubmitJobResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: SubmitJobRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Response[Union[Any, SubmitJobResponse]]:
    """Submit a new job

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (SubmitJobRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, SubmitJobResponse]]
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
    body: SubmitJobRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Optional[Union[Any, SubmitJobResponse]]:
    """Submit a new job

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (SubmitJobRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, SubmitJobResponse]
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
    body: SubmitJobRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Response[Union[Any, SubmitJobResponse]]:
    """Submit a new job

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (SubmitJobRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, SubmitJobResponse]]
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
    body: SubmitJobRequest,
    x_tenant_id: str,
    idempotency_key: Union[Unset, UUID] = UNSET,
) -> Optional[Union[Any, SubmitJobResponse]]:
    """Submit a new job

    Args:
        x_tenant_id (str):
        idempotency_key (Union[Unset, UUID]):
        body (SubmitJobRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, SubmitJobResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
            idempotency_key=idempotency_key,
        )
    ).parsed
