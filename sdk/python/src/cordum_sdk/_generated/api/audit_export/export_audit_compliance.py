from http import HTTPStatus
from typing import Any, Dict, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.error import Error
from ...models.export_audit_compliance_format import ExportAuditComplianceFormat
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union
import datetime



def _get_kwargs(
    *,
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    format_: Union[Unset, ExportAuditComplianceFormat] = ExportAuditComplianceFormat.NDJSON,
    max_events: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id





    params: Dict[str, Any] = {}

    json_from_: Union[Unset, str] = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_to: Union[Unset, str] = UNSET
    if not isinstance(to, Unset):
        json_to = to.isoformat()
    params["to"] = json_to

    json_format_: Union[Unset, str] = UNSET
    if not isinstance(format_, Unset):
        json_format_ = format_.value

    params["format"] = json_format_

    params["max_events"] = max_events


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/audit/export",
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, Error, str]]:
    if response.status_code == 200:
        response_200 = response.text
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
    if response.status_code == 503:
        response_503 = Error.from_dict(response.json())



        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, Error, str]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    format_: Union[Unset, ExportAuditComplianceFormat] = ExportAuditComplianceFormat.NDJSON,
    max_events: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, Error, str]]:
    """ Stream compliance audit export

     Streams a compliance-shaped audit export (ndjson or csv) for a bounded time window.
    Admin-only; gated by the `siem_export` or `audit_export` entitlement. The response is
    retention-aware: windows older than the configured retention + skew are rejected. The
    body carries SOC2 mapping columns when the operator has configured a mapping table.

    Args:
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        format_ (Union[Unset, ExportAuditComplianceFormat]):  Default:
            ExportAuditComplianceFormat.NDJSON.
        max_events (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, Error, str]]
     """


    kwargs = _get_kwargs(
        from_=from_,
to=to,
format_=format_,
max_events=max_events,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    format_: Union[Unset, ExportAuditComplianceFormat] = ExportAuditComplianceFormat.NDJSON,
    max_events: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, Error, str]]:
    """ Stream compliance audit export

     Streams a compliance-shaped audit export (ndjson or csv) for a bounded time window.
    Admin-only; gated by the `siem_export` or `audit_export` entitlement. The response is
    retention-aware: windows older than the configured retention + skew are rejected. The
    body carries SOC2 mapping columns when the operator has configured a mapping table.

    Args:
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        format_ (Union[Unset, ExportAuditComplianceFormat]):  Default:
            ExportAuditComplianceFormat.NDJSON.
        max_events (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, Error, str]
     """


    return sync_detailed(
        client=client,
from_=from_,
to=to,
format_=format_,
max_events=max_events,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    format_: Union[Unset, ExportAuditComplianceFormat] = ExportAuditComplianceFormat.NDJSON,
    max_events: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, Error, str]]:
    """ Stream compliance audit export

     Streams a compliance-shaped audit export (ndjson or csv) for a bounded time window.
    Admin-only; gated by the `siem_export` or `audit_export` entitlement. The response is
    retention-aware: windows older than the configured retention + skew are rejected. The
    body carries SOC2 mapping columns when the operator has configured a mapping table.

    Args:
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        format_ (Union[Unset, ExportAuditComplianceFormat]):  Default:
            ExportAuditComplianceFormat.NDJSON.
        max_events (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, Error, str]]
     """


    kwargs = _get_kwargs(
        from_=from_,
to=to,
format_=format_,
max_events=max_events,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    from_: Union[Unset, datetime.datetime] = UNSET,
    to: Union[Unset, datetime.datetime] = UNSET,
    format_: Union[Unset, ExportAuditComplianceFormat] = ExportAuditComplianceFormat.NDJSON,
    max_events: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, Error, str]]:
    """ Stream compliance audit export

     Streams a compliance-shaped audit export (ndjson or csv) for a bounded time window.
    Admin-only; gated by the `siem_export` or `audit_export` entitlement. The response is
    retention-aware: windows older than the configured retention + skew are rejected. The
    body carries SOC2 mapping columns when the operator has configured a mapping table.

    Args:
        from_ (Union[Unset, datetime.datetime]):
        to (Union[Unset, datetime.datetime]):
        format_ (Union[Unset, ExportAuditComplianceFormat]):  Default:
            ExportAuditComplianceFormat.NDJSON.
        max_events (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, Error, str]
     """


    return (await asyncio_detailed(
        client=client,
from_=from_,
to=to,
format_=format_,
max_events=max_events,
x_tenant_id=x_tenant_id,

    )).parsed
