from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.eval_runs_response import EvalRunsResponse
from ...types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime



def _get_kwargs(
    id: str,
    *,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    has_regression: Union[Unset, bool] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    min_score: Union[Unset, float] = UNSET,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    params: Dict[str, Any] = {}

    params["cursor"] = cursor

    params["limit"] = limit

    params["has_regression"] = has_regression

    json_since: Union[Unset, str] = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: Union[Unset, str] = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params["min_score"] = min_score


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/evals/datasets/{id}/runs".format(id=id,),
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, EvalRunsResponse]]:
    if response.status_code == 200:
        response_200 = EvalRunsResponse.from_dict(response.json())



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
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, EvalRunsResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    has_regression: Union[Unset, bool] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    min_score: Union[Unset, float] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, EvalRunsResponse]]:
    """ List historical runs for one eval dataset

    Args:
        id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        has_regression (Union[Unset, bool]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        min_score (Union[Unset, float]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EvalRunsResponse]]
     """


    kwargs = _get_kwargs(
        id=id,
cursor=cursor,
limit=limit,
has_regression=has_regression,
since=since,
until=until,
min_score=min_score,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    has_regression: Union[Unset, bool] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    min_score: Union[Unset, float] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, EvalRunsResponse]]:
    """ List historical runs for one eval dataset

    Args:
        id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        has_regression (Union[Unset, bool]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        min_score (Union[Unset, float]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EvalRunsResponse]
     """


    return sync_detailed(
        id=id,
client=client,
cursor=cursor,
limit=limit,
has_regression=has_regression,
since=since,
until=until,
min_score=min_score,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    has_regression: Union[Unset, bool] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    min_score: Union[Unset, float] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, EvalRunsResponse]]:
    """ List historical runs for one eval dataset

    Args:
        id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        has_regression (Union[Unset, bool]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        min_score (Union[Unset, float]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EvalRunsResponse]]
     """


    kwargs = _get_kwargs(
        id=id,
cursor=cursor,
limit=limit,
has_regression=has_regression,
since=since,
until=until,
min_score=min_score,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    has_regression: Union[Unset, bool] = UNSET,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    min_score: Union[Unset, float] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, EvalRunsResponse]]:
    """ List historical runs for one eval dataset

    Args:
        id (str):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        has_regression (Union[Unset, bool]):
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        min_score (Union[Unset, float]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EvalRunsResponse]
     """


    return (await asyncio_detailed(
        id=id,
client=client,
cursor=cursor,
limit=limit,
has_regression=has_regression,
since=since,
until=until,
min_score=min_score,
x_tenant_id=x_tenant_id,

    )).parsed
