from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.list_shadow_agent_findings_ci_provider import ListShadowAgentFindingsCiProvider
from ...models.list_shadow_agent_findings_risk import ListShadowAgentFindingsRisk
from ...models.list_shadow_agent_findings_source_type import ListShadowAgentFindingsSourceType
from ...models.list_shadow_agent_findings_status import ListShadowAgentFindingsStatus
from ...models.shadow_agent_finding_page import ShadowAgentFindingPage
from ...types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime


def _get_kwargs(
    *,
    status: Union[Unset, ListShadowAgentFindingsStatus] = UNSET,
    risk: Union[Unset, ListShadowAgentFindingsRisk] = UNSET,
    agent: Union[Unset, str] = UNSET,
    agent_product: Union[Unset, str] = UNSET,
    owner: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    cursor: Union[Unset, str] = UNSET,
    source_type: Union[Unset, ListShadowAgentFindingsSourceType] = UNSET,
    cluster_id: Union[Unset, str] = UNSET,
    namespace: Union[Unset, str] = UNSET,
    ci_provider: Union[Unset, ListShadowAgentFindingsCiProvider] = UNSET,
    repo: Union[Unset, str] = UNSET,
    signal: Union[Unset, List[str]] = UNSET,
    confidence_min: Union[Unset, float] = UNSET,
    first_seen_after: Union[Unset, datetime.datetime] = UNSET,
    last_seen_before: Union[Unset, datetime.datetime] = UNSET,
    exception_id: Union[Unset, str] = UNSET,
    include_managed_skip: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    json_status: Union[Unset, str] = UNSET
    if not isinstance(status, Unset):
        json_status = status.value

    params["status"] = json_status

    json_risk: Union[Unset, str] = UNSET
    if not isinstance(risk, Unset):
        json_risk = risk.value

    params["risk"] = json_risk

    params["agent"] = agent

    params["agent_product"] = agent_product

    params["owner"] = owner

    params["limit"] = limit

    params["cursor"] = cursor

    json_source_type: Union[Unset, str] = UNSET
    if not isinstance(source_type, Unset):
        json_source_type = source_type.value

    params["source_type"] = json_source_type

    params["cluster_id"] = cluster_id

    params["namespace"] = namespace

    json_ci_provider: Union[Unset, str] = UNSET
    if not isinstance(ci_provider, Unset):
        json_ci_provider = ci_provider.value

    params["ci_provider"] = json_ci_provider

    params["repo"] = repo

    json_signal: Union[Unset, List[str]] = UNSET
    if not isinstance(signal, Unset):
        json_signal = signal

    params["signal"] = json_signal

    params["confidence_min"] = confidence_min

    json_first_seen_after: Union[Unset, str] = UNSET
    if not isinstance(first_seen_after, Unset):
        json_first_seen_after = first_seen_after.isoformat()
    params["first_seen_after"] = json_first_seen_after

    json_last_seen_before: Union[Unset, str] = UNSET
    if not isinstance(last_seen_before, Unset):
        json_last_seen_before = last_seen_before.isoformat()
    params["last_seen_before"] = json_last_seen_before

    params["exception_id"] = exception_id

    params["include_managed_skip"] = include_managed_skip

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/shadow-agents",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, ShadowAgentFindingPage]]:
    if response.status_code == 200:
        response_200 = ShadowAgentFindingPage.from_dict(response.json())

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
) -> Response[Union[Any, ShadowAgentFindingPage]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowAgentFindingsStatus] = UNSET,
    risk: Union[Unset, ListShadowAgentFindingsRisk] = UNSET,
    agent: Union[Unset, str] = UNSET,
    agent_product: Union[Unset, str] = UNSET,
    owner: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    cursor: Union[Unset, str] = UNSET,
    source_type: Union[Unset, ListShadowAgentFindingsSourceType] = UNSET,
    cluster_id: Union[Unset, str] = UNSET,
    namespace: Union[Unset, str] = UNSET,
    ci_provider: Union[Unset, ListShadowAgentFindingsCiProvider] = UNSET,
    repo: Union[Unset, str] = UNSET,
    signal: Union[Unset, List[str]] = UNSET,
    confidence_min: Union[Unset, float] = UNSET,
    first_seen_after: Union[Unset, datetime.datetime] = UNSET,
    last_seen_before: Union[Unset, datetime.datetime] = UNSET,
    exception_id: Union[Unset, str] = UNSET,
    include_managed_skip: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Response[Union[Any, ShadowAgentFindingPage]]:
    """List shadow-agent findings

     Returns a tenant-scoped cursor-paginated page of ShadowAgentFinding records. Filters narrow the
    result set; the narrowest filter is selected as the primary index path. Resolved/suppressed records
    past terminal retention are hidden.

    Args:
        status (Union[Unset, ListShadowAgentFindingsStatus]):
        risk (Union[Unset, ListShadowAgentFindingsRisk]):
        agent (Union[Unset, str]):
        agent_product (Union[Unset, str]):
        owner (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        cursor (Union[Unset, str]):
        source_type (Union[Unset, ListShadowAgentFindingsSourceType]):
        cluster_id (Union[Unset, str]):
        namespace (Union[Unset, str]):
        ci_provider (Union[Unset, ListShadowAgentFindingsCiProvider]):
        repo (Union[Unset, str]):
        signal (Union[Unset, List[str]]):
        confidence_min (Union[Unset, float]):
        first_seen_after (Union[Unset, datetime.datetime]):
        last_seen_before (Union[Unset, datetime.datetime]):
        exception_id (Union[Unset, str]):
        include_managed_skip (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowAgentFindingPage]]
    """

    kwargs = _get_kwargs(
        status=status,
        risk=risk,
        agent=agent,
        agent_product=agent_product,
        owner=owner,
        limit=limit,
        cursor=cursor,
        source_type=source_type,
        cluster_id=cluster_id,
        namespace=namespace,
        ci_provider=ci_provider,
        repo=repo,
        signal=signal,
        confidence_min=confidence_min,
        first_seen_after=first_seen_after,
        last_seen_before=last_seen_before,
        exception_id=exception_id,
        include_managed_skip=include_managed_skip,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowAgentFindingsStatus] = UNSET,
    risk: Union[Unset, ListShadowAgentFindingsRisk] = UNSET,
    agent: Union[Unset, str] = UNSET,
    agent_product: Union[Unset, str] = UNSET,
    owner: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    cursor: Union[Unset, str] = UNSET,
    source_type: Union[Unset, ListShadowAgentFindingsSourceType] = UNSET,
    cluster_id: Union[Unset, str] = UNSET,
    namespace: Union[Unset, str] = UNSET,
    ci_provider: Union[Unset, ListShadowAgentFindingsCiProvider] = UNSET,
    repo: Union[Unset, str] = UNSET,
    signal: Union[Unset, List[str]] = UNSET,
    confidence_min: Union[Unset, float] = UNSET,
    first_seen_after: Union[Unset, datetime.datetime] = UNSET,
    last_seen_before: Union[Unset, datetime.datetime] = UNSET,
    exception_id: Union[Unset, str] = UNSET,
    include_managed_skip: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowAgentFindingPage]]:
    """List shadow-agent findings

     Returns a tenant-scoped cursor-paginated page of ShadowAgentFinding records. Filters narrow the
    result set; the narrowest filter is selected as the primary index path. Resolved/suppressed records
    past terminal retention are hidden.

    Args:
        status (Union[Unset, ListShadowAgentFindingsStatus]):
        risk (Union[Unset, ListShadowAgentFindingsRisk]):
        agent (Union[Unset, str]):
        agent_product (Union[Unset, str]):
        owner (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        cursor (Union[Unset, str]):
        source_type (Union[Unset, ListShadowAgentFindingsSourceType]):
        cluster_id (Union[Unset, str]):
        namespace (Union[Unset, str]):
        ci_provider (Union[Unset, ListShadowAgentFindingsCiProvider]):
        repo (Union[Unset, str]):
        signal (Union[Unset, List[str]]):
        confidence_min (Union[Unset, float]):
        first_seen_after (Union[Unset, datetime.datetime]):
        last_seen_before (Union[Unset, datetime.datetime]):
        exception_id (Union[Unset, str]):
        include_managed_skip (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowAgentFindingPage]
    """

    return sync_detailed(
        client=client,
        status=status,
        risk=risk,
        agent=agent,
        agent_product=agent_product,
        owner=owner,
        limit=limit,
        cursor=cursor,
        source_type=source_type,
        cluster_id=cluster_id,
        namespace=namespace,
        ci_provider=ci_provider,
        repo=repo,
        signal=signal,
        confidence_min=confidence_min,
        first_seen_after=first_seen_after,
        last_seen_before=last_seen_before,
        exception_id=exception_id,
        include_managed_skip=include_managed_skip,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowAgentFindingsStatus] = UNSET,
    risk: Union[Unset, ListShadowAgentFindingsRisk] = UNSET,
    agent: Union[Unset, str] = UNSET,
    agent_product: Union[Unset, str] = UNSET,
    owner: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    cursor: Union[Unset, str] = UNSET,
    source_type: Union[Unset, ListShadowAgentFindingsSourceType] = UNSET,
    cluster_id: Union[Unset, str] = UNSET,
    namespace: Union[Unset, str] = UNSET,
    ci_provider: Union[Unset, ListShadowAgentFindingsCiProvider] = UNSET,
    repo: Union[Unset, str] = UNSET,
    signal: Union[Unset, List[str]] = UNSET,
    confidence_min: Union[Unset, float] = UNSET,
    first_seen_after: Union[Unset, datetime.datetime] = UNSET,
    last_seen_before: Union[Unset, datetime.datetime] = UNSET,
    exception_id: Union[Unset, str] = UNSET,
    include_managed_skip: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Response[Union[Any, ShadowAgentFindingPage]]:
    """List shadow-agent findings

     Returns a tenant-scoped cursor-paginated page of ShadowAgentFinding records. Filters narrow the
    result set; the narrowest filter is selected as the primary index path. Resolved/suppressed records
    past terminal retention are hidden.

    Args:
        status (Union[Unset, ListShadowAgentFindingsStatus]):
        risk (Union[Unset, ListShadowAgentFindingsRisk]):
        agent (Union[Unset, str]):
        agent_product (Union[Unset, str]):
        owner (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        cursor (Union[Unset, str]):
        source_type (Union[Unset, ListShadowAgentFindingsSourceType]):
        cluster_id (Union[Unset, str]):
        namespace (Union[Unset, str]):
        ci_provider (Union[Unset, ListShadowAgentFindingsCiProvider]):
        repo (Union[Unset, str]):
        signal (Union[Unset, List[str]]):
        confidence_min (Union[Unset, float]):
        first_seen_after (Union[Unset, datetime.datetime]):
        last_seen_before (Union[Unset, datetime.datetime]):
        exception_id (Union[Unset, str]):
        include_managed_skip (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowAgentFindingPage]]
    """

    kwargs = _get_kwargs(
        status=status,
        risk=risk,
        agent=agent,
        agent_product=agent_product,
        owner=owner,
        limit=limit,
        cursor=cursor,
        source_type=source_type,
        cluster_id=cluster_id,
        namespace=namespace,
        ci_provider=ci_provider,
        repo=repo,
        signal=signal,
        confidence_min=confidence_min,
        first_seen_after=first_seen_after,
        last_seen_before=last_seen_before,
        exception_id=exception_id,
        include_managed_skip=include_managed_skip,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowAgentFindingsStatus] = UNSET,
    risk: Union[Unset, ListShadowAgentFindingsRisk] = UNSET,
    agent: Union[Unset, str] = UNSET,
    agent_product: Union[Unset, str] = UNSET,
    owner: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    cursor: Union[Unset, str] = UNSET,
    source_type: Union[Unset, ListShadowAgentFindingsSourceType] = UNSET,
    cluster_id: Union[Unset, str] = UNSET,
    namespace: Union[Unset, str] = UNSET,
    ci_provider: Union[Unset, ListShadowAgentFindingsCiProvider] = UNSET,
    repo: Union[Unset, str] = UNSET,
    signal: Union[Unset, List[str]] = UNSET,
    confidence_min: Union[Unset, float] = UNSET,
    first_seen_after: Union[Unset, datetime.datetime] = UNSET,
    last_seen_before: Union[Unset, datetime.datetime] = UNSET,
    exception_id: Union[Unset, str] = UNSET,
    include_managed_skip: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowAgentFindingPage]]:
    """List shadow-agent findings

     Returns a tenant-scoped cursor-paginated page of ShadowAgentFinding records. Filters narrow the
    result set; the narrowest filter is selected as the primary index path. Resolved/suppressed records
    past terminal retention are hidden.

    Args:
        status (Union[Unset, ListShadowAgentFindingsStatus]):
        risk (Union[Unset, ListShadowAgentFindingsRisk]):
        agent (Union[Unset, str]):
        agent_product (Union[Unset, str]):
        owner (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        cursor (Union[Unset, str]):
        source_type (Union[Unset, ListShadowAgentFindingsSourceType]):
        cluster_id (Union[Unset, str]):
        namespace (Union[Unset, str]):
        ci_provider (Union[Unset, ListShadowAgentFindingsCiProvider]):
        repo (Union[Unset, str]):
        signal (Union[Unset, List[str]]):
        confidence_min (Union[Unset, float]):
        first_seen_after (Union[Unset, datetime.datetime]):
        last_seen_before (Union[Unset, datetime.datetime]):
        exception_id (Union[Unset, str]):
        include_managed_skip (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowAgentFindingPage]
    """

    return (
        await asyncio_detailed(
            client=client,
            status=status,
            risk=risk,
            agent=agent,
            agent_product=agent_product,
            owner=owner,
            limit=limit,
            cursor=cursor,
            source_type=source_type,
            cluster_id=cluster_id,
            namespace=namespace,
            ci_provider=ci_provider,
            repo=repo,
            signal=signal,
            confidence_min=confidence_min,
            first_seen_after=first_seen_after,
            last_seen_before=last_seen_before,
            exception_id=exception_id,
            include_managed_skip=include_managed_skip,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
