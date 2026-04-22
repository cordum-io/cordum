from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.approval_analytics_response import ApprovalAnalyticsResponse
from ...models.error import Error
from ...models.get_approval_analytics_group_by import GetApprovalAnalyticsGroupBy
from ...models.get_approval_analytics_window import GetApprovalAnalyticsWindow
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union



def _get_kwargs(
    *,
    window: Union[Unset, GetApprovalAnalyticsWindow] = UNSET,
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    group_by: Union[Unset, GetApprovalAnalyticsGroupBy] = GetApprovalAnalyticsGroupBy.OVERALL,
    limit: Union[Unset, int] = 10,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    params: Dict[str, Any] = {}

    json_window: Union[Unset, str] = UNSET
    if not isinstance(window, Unset):
        json_window = window.value

    params["window"] = json_window

    params["since"] = since

    params["until"] = until

    json_group_by: Union[Unset, str] = UNSET
    if not isinstance(group_by, Unset):
        json_group_by = group_by.value

    params["group_by"] = json_group_by

    params["limit"] = limit


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/governance/approvals/analytics",
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, ApprovalAnalyticsResponse, Error]]:
    if response.status_code == 200:
        response_200 = ApprovalAnalyticsResponse.from_dict(response.json())



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
        response_503 = Error.from_dict(response.json())



        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, ApprovalAnalyticsResponse, Error]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    window: Union[Unset, GetApprovalAnalyticsWindow] = UNSET,
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    group_by: Union[Unset, GetApprovalAnalyticsGroupBy] = GetApprovalAnalyticsGroupBy.OVERALL,
    limit: Union[Unset, int] = 10,
    x_tenant_id: str,

) -> Response[Union[Any, ApprovalAnalyticsResponse, Error]]:
    """ Approval analytics (time-to-approve, auto vs manual, bottlenecks)

     Aggregated approval KPIs for the requested window. Consumes the
    Policy Decision Log as the source of truth, pairs each
    require_approval verdict with its ApprovalRecord, and computes
    per-group breakdowns sorted with the slowest approvers first so
    bottlenecks surface at the top.

    Requires the `governance.read` permission.

    Responses are memoised per `(tenant, window, group_by, limit)`
    tuple for 30 seconds so dashboards can poll without thrashing
    the decision-log index.

    Args:
        window (Union[Unset, GetApprovalAnalyticsWindow]):
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        group_by (Union[Unset, GetApprovalAnalyticsGroupBy]):  Default:
            GetApprovalAnalyticsGroupBy.OVERALL.
        limit (Union[Unset, int]):  Default: 10.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ApprovalAnalyticsResponse, Error]]
     """


    kwargs = _get_kwargs(
        window=window,
since=since,
until=until,
group_by=group_by,
limit=limit,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: AuthenticatedClient,
    window: Union[Unset, GetApprovalAnalyticsWindow] = UNSET,
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    group_by: Union[Unset, GetApprovalAnalyticsGroupBy] = GetApprovalAnalyticsGroupBy.OVERALL,
    limit: Union[Unset, int] = 10,
    x_tenant_id: str,

) -> Optional[Union[Any, ApprovalAnalyticsResponse, Error]]:
    """ Approval analytics (time-to-approve, auto vs manual, bottlenecks)

     Aggregated approval KPIs for the requested window. Consumes the
    Policy Decision Log as the source of truth, pairs each
    require_approval verdict with its ApprovalRecord, and computes
    per-group breakdowns sorted with the slowest approvers first so
    bottlenecks surface at the top.

    Requires the `governance.read` permission.

    Responses are memoised per `(tenant, window, group_by, limit)`
    tuple for 30 seconds so dashboards can poll without thrashing
    the decision-log index.

    Args:
        window (Union[Unset, GetApprovalAnalyticsWindow]):
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        group_by (Union[Unset, GetApprovalAnalyticsGroupBy]):  Default:
            GetApprovalAnalyticsGroupBy.OVERALL.
        limit (Union[Unset, int]):  Default: 10.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ApprovalAnalyticsResponse, Error]
     """


    return sync_detailed(
        client=client,
window=window,
since=since,
until=until,
group_by=group_by,
limit=limit,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    window: Union[Unset, GetApprovalAnalyticsWindow] = UNSET,
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    group_by: Union[Unset, GetApprovalAnalyticsGroupBy] = GetApprovalAnalyticsGroupBy.OVERALL,
    limit: Union[Unset, int] = 10,
    x_tenant_id: str,

) -> Response[Union[Any, ApprovalAnalyticsResponse, Error]]:
    """ Approval analytics (time-to-approve, auto vs manual, bottlenecks)

     Aggregated approval KPIs for the requested window. Consumes the
    Policy Decision Log as the source of truth, pairs each
    require_approval verdict with its ApprovalRecord, and computes
    per-group breakdowns sorted with the slowest approvers first so
    bottlenecks surface at the top.

    Requires the `governance.read` permission.

    Responses are memoised per `(tenant, window, group_by, limit)`
    tuple for 30 seconds so dashboards can poll without thrashing
    the decision-log index.

    Args:
        window (Union[Unset, GetApprovalAnalyticsWindow]):
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        group_by (Union[Unset, GetApprovalAnalyticsGroupBy]):  Default:
            GetApprovalAnalyticsGroupBy.OVERALL.
        limit (Union[Unset, int]):  Default: 10.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ApprovalAnalyticsResponse, Error]]
     """


    kwargs = _get_kwargs(
        window=window,
since=since,
until=until,
group_by=group_by,
limit=limit,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: AuthenticatedClient,
    window: Union[Unset, GetApprovalAnalyticsWindow] = UNSET,
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    group_by: Union[Unset, GetApprovalAnalyticsGroupBy] = GetApprovalAnalyticsGroupBy.OVERALL,
    limit: Union[Unset, int] = 10,
    x_tenant_id: str,

) -> Optional[Union[Any, ApprovalAnalyticsResponse, Error]]:
    """ Approval analytics (time-to-approve, auto vs manual, bottlenecks)

     Aggregated approval KPIs for the requested window. Consumes the
    Policy Decision Log as the source of truth, pairs each
    require_approval verdict with its ApprovalRecord, and computes
    per-group breakdowns sorted with the slowest approvers first so
    bottlenecks surface at the top.

    Requires the `governance.read` permission.

    Responses are memoised per `(tenant, window, group_by, limit)`
    tuple for 30 seconds so dashboards can poll without thrashing
    the decision-log index.

    Args:
        window (Union[Unset, GetApprovalAnalyticsWindow]):
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        group_by (Union[Unset, GetApprovalAnalyticsGroupBy]):  Default:
            GetApprovalAnalyticsGroupBy.OVERALL.
        limit (Union[Unset, int]):  Default: 10.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ApprovalAnalyticsResponse, Error]
     """


    return (await asyncio_detailed(
        client=client,
window=window,
since=since,
until=until,
group_by=group_by,
limit=limit,
x_tenant_id=x_tenant_id,

    )).parsed
