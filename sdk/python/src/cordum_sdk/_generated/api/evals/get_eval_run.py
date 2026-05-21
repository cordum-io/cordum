from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.eval_run_accepted_response import EvalRunAcceptedResponse
from ...models.eval_run_result import EvalRunResult
from typing import cast
from typing import Dict


def _get_kwargs(
    run_id: str,
    *,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/evals/runs/{run_id}".format(
            run_id=run_id,
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]:
    if response.status_code == 200:
        response_200 = EvalRunResult.from_dict(response.json())

        return response_200
    if response.status_code == 202:
        response_202 = EvalRunAcceptedResponse.from_dict(response.json())

        return response_202
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
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    run_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]:
    """Get one eval run

    Args:
        run_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    run_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]:
    """Get one eval run

    Args:
        run_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EvalRunAcceptedResponse, EvalRunResult]
    """

    return sync_detailed(
        run_id=run_id,
        client=client,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    run_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]:
    """Get one eval run

    Args:
        run_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]
    """

    kwargs = _get_kwargs(
        run_id=run_id,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    run_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, EvalRunAcceptedResponse, EvalRunResult]]:
    """Get one eval run

    Args:
        run_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EvalRunAcceptedResponse, EvalRunResult]
    """

    return (
        await asyncio_detailed(
            run_id=run_id,
            client=client,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
