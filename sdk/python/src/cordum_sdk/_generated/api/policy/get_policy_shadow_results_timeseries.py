from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.get_policy_shadow_results_timeseries_bucket import (
    GetPolicyShadowResultsTimeseriesBucket,
)
from ...models.shadow_timeseries_response import ShadowTimeseriesResponse
from typing import cast
from typing import Dict


def _get_kwargs(
    id: str,
    *,
    from_: int,
    to: int,
    bucket: GetPolicyShadowResultsTimeseriesBucket,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["from"] = from_

    params["to"] = to

    json_bucket = bucket.value
    params["bucket"] = json_bucket

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/policy/shadows/{id}/results/timeseries".format(
            id=id,
        ),
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, ShadowTimeseriesResponse]]:
    if response.status_code == 200:
        response_200 = ShadowTimeseriesResponse.from_dict(response.json())

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
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, ShadowTimeseriesResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient,
    from_: int,
    to: int,
    bucket: GetPolicyShadowResultsTimeseriesBucket,
    x_tenant_id: str,
) -> Response[Union[Any, ShadowTimeseriesResponse]]:
    """Zero-filled bucketed timeseries of shadow outcomes

     Returns one row per bucket (including empty buckets) so the
    dashboard chart shows gaps rather than stretching the last
    observation. Bucket widths are a closed whitelist to prevent
    fine-grained requests blowing out the 2000-bucket ceiling.

    Args:
        id (str):
        from_ (int):
        to (int):
        bucket (GetPolicyShadowResultsTimeseriesBucket):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowTimeseriesResponse]]
    """

    kwargs = _get_kwargs(
        id=id,
        from_=from_,
        to=to,
        bucket=bucket,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient,
    from_: int,
    to: int,
    bucket: GetPolicyShadowResultsTimeseriesBucket,
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowTimeseriesResponse]]:
    """Zero-filled bucketed timeseries of shadow outcomes

     Returns one row per bucket (including empty buckets) so the
    dashboard chart shows gaps rather than stretching the last
    observation. Bucket widths are a closed whitelist to prevent
    fine-grained requests blowing out the 2000-bucket ceiling.

    Args:
        id (str):
        from_ (int):
        to (int):
        bucket (GetPolicyShadowResultsTimeseriesBucket):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowTimeseriesResponse]
    """

    return sync_detailed(
        id=id,
        client=client,
        from_=from_,
        to=to,
        bucket=bucket,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient,
    from_: int,
    to: int,
    bucket: GetPolicyShadowResultsTimeseriesBucket,
    x_tenant_id: str,
) -> Response[Union[Any, ShadowTimeseriesResponse]]:
    """Zero-filled bucketed timeseries of shadow outcomes

     Returns one row per bucket (including empty buckets) so the
    dashboard chart shows gaps rather than stretching the last
    observation. Bucket widths are a closed whitelist to prevent
    fine-grained requests blowing out the 2000-bucket ceiling.

    Args:
        id (str):
        from_ (int):
        to (int):
        bucket (GetPolicyShadowResultsTimeseriesBucket):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowTimeseriesResponse]]
    """

    kwargs = _get_kwargs(
        id=id,
        from_=from_,
        to=to,
        bucket=bucket,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient,
    from_: int,
    to: int,
    bucket: GetPolicyShadowResultsTimeseriesBucket,
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowTimeseriesResponse]]:
    """Zero-filled bucketed timeseries of shadow outcomes

     Returns one row per bucket (including empty buckets) so the
    dashboard chart shows gaps rather than stretching the last
    observation. Bucket widths are a closed whitelist to prevent
    fine-grained requests blowing out the 2000-bucket ceiling.

    Args:
        id (str):
        from_ (int):
        to (int):
        bucket (GetPolicyShadowResultsTimeseriesBucket):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowTimeseriesResponse]
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            from_=from_,
            to=to,
            bucket=bucket,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
