from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.list_governance_decisions_response_200 import ListGovernanceDecisionsResponse200
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union



def _get_kwargs(
    *,
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    topic: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    verdict: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    params: Dict[str, Any] = {}

    params["since"] = since

    params["until"] = until

    params["topic"] = topic

    params["rule_id"] = rule_id

    params["verdict"] = verdict

    params["agent_id"] = agent_id

    params["cursor"] = cursor

    params["limit"] = limit


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/governance/decisions",
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, ListGovernanceDecisionsResponse200]]:
    if response.status_code == 200:
        response_200 = ListGovernanceDecisionsResponse200.from_dict(response.json())



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


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, ListGovernanceDecisionsResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    topic: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    verdict: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, ListGovernanceDecisionsResponse200]]:
    """ Query the governance decision log

    Args:
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        topic (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        verdict (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListGovernanceDecisionsResponse200]]
     """


    kwargs = _get_kwargs(
        since=since,
until=until,
topic=topic,
rule_id=rule_id,
verdict=verdict,
agent_id=agent_id,
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
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    topic: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    verdict: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, ListGovernanceDecisionsResponse200]]:
    """ Query the governance decision log

    Args:
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        topic (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        verdict (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListGovernanceDecisionsResponse200]
     """


    return sync_detailed(
        client=client,
since=since,
until=until,
topic=topic,
rule_id=rule_id,
verdict=verdict,
agent_id=agent_id,
cursor=cursor,
limit=limit,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    topic: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    verdict: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, ListGovernanceDecisionsResponse200]]:
    """ Query the governance decision log

    Args:
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        topic (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        verdict (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListGovernanceDecisionsResponse200]]
     """


    kwargs = _get_kwargs(
        since=since,
until=until,
topic=topic,
rule_id=rule_id,
verdict=verdict,
agent_id=agent_id,
cursor=cursor,
limit=limit,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    since: Union[Unset, str] = UNSET,
    until: Union[Unset, str] = UNSET,
    topic: Union[Unset, str] = UNSET,
    rule_id: Union[Unset, str] = UNSET,
    verdict: Union[Unset, str] = UNSET,
    agent_id: Union[Unset, str] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, ListGovernanceDecisionsResponse200]]:
    """ Query the governance decision log

    Args:
        since (Union[Unset, str]):
        until (Union[Unset, str]):
        topic (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        verdict (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListGovernanceDecisionsResponse200]
     """


    return (await asyncio_detailed(
        client=client,
since=since,
until=until,
topic=topic,
rule_id=rule_id,
verdict=verdict,
agent_id=agent_id,
cursor=cursor,
limit=limit,
x_tenant_id=x_tenant_id,

    )).parsed
