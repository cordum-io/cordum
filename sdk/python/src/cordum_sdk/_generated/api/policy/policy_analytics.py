from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.policy_analytics_body import PolicyAnalyticsBody
from ...models.policy_analytics_response_200 import PolicyAnalyticsResponse200
from typing import cast
from typing import Dict


def _get_kwargs(
    *,
    body: PolicyAnalyticsBody,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/policy/analytics",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, PolicyAnalyticsResponse200]]:
    if response.status_code == 200:
        response_200 = PolicyAnalyticsResponse200.from_dict(response.json())

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
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, PolicyAnalyticsResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: PolicyAnalyticsBody,
) -> Response[Union[Any, PolicyAnalyticsResponse200]]:
    """Compute per-rule quality analytics (false positive rates, approval fatigue)

     Analyzes policy rule quality over a time range. Returns per-rule metrics including hit counts,
    override rates, approval latency, and daily trends. Read-only — no side effects.

    Args:
        body (PolicyAnalyticsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PolicyAnalyticsResponse200]]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: PolicyAnalyticsBody,
) -> Optional[Union[Any, PolicyAnalyticsResponse200]]:
    """Compute per-rule quality analytics (false positive rates, approval fatigue)

     Analyzes policy rule quality over a time range. Returns per-rule metrics including hit counts,
    override rates, approval latency, and daily trends. Read-only — no side effects.

    Args:
        body (PolicyAnalyticsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PolicyAnalyticsResponse200]
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: PolicyAnalyticsBody,
) -> Response[Union[Any, PolicyAnalyticsResponse200]]:
    """Compute per-rule quality analytics (false positive rates, approval fatigue)

     Analyzes policy rule quality over a time range. Returns per-rule metrics including hit counts,
    override rates, approval latency, and daily trends. Read-only — no side effects.

    Args:
        body (PolicyAnalyticsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, PolicyAnalyticsResponse200]]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: PolicyAnalyticsBody,
) -> Optional[Union[Any, PolicyAnalyticsResponse200]]:
    """Compute per-rule quality analytics (false positive rates, approval fatigue)

     Analyzes policy rule quality over a time range. Returns per-rule metrics including hit counts,
    override rates, approval latency, and daily trends. Read-only — no side effects.

    Args:
        body (PolicyAnalyticsBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, PolicyAnalyticsResponse200]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
