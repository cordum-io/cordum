from http import HTTPStatus
from typing import Any, Dict, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response
from ... import errors

from ...models.edge_calibration_report import EdgeCalibrationReport
from ...models.edge_calibration_report_request import EdgeCalibrationReportRequest
from typing import cast
from typing import Dict


def _get_kwargs(
    *,
    body: EdgeCalibrationReportRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/calibration-reports",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeCalibrationReport]]:
    if response.status_code == 201:
        response_201 = EdgeCalibrationReport.from_dict(response.json())

        return response_201
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
) -> Response[Union[Any, EdgeCalibrationReport]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeCalibrationReportRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeCalibrationReport]]:
    """Generate an Edge calibration report

     Summarizes the policy decisions recorded on the Edge events of the
    tenant's observe-mode sessions in the window [since, until), narrowed
    to the given repositories and labels when set. The window may not
    exceed 90 days. The report is stored under its content-derived id and
    returned. Requires policy.write or the admin role.

    Args:
        x_tenant_id (str):
        body (EdgeCalibrationReportRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeCalibrationReport]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeCalibrationReportRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeCalibrationReport]]:
    """Generate an Edge calibration report

     Summarizes the policy decisions recorded on the Edge events of the
    tenant's observe-mode sessions in the window [since, until), narrowed
    to the given repositories and labels when set. The window may not
    exceed 90 days. The report is stored under its content-derived id and
    returned. Requires policy.write or the admin role.

    Args:
        x_tenant_id (str):
        body (EdgeCalibrationReportRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeCalibrationReport]
    """

    return sync_detailed(
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeCalibrationReportRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeCalibrationReport]]:
    """Generate an Edge calibration report

     Summarizes the policy decisions recorded on the Edge events of the
    tenant's observe-mode sessions in the window [since, until), narrowed
    to the given repositories and labels when set. The window may not
    exceed 90 days. The report is stored under its content-derived id and
    returned. Requires policy.write or the admin role.

    Args:
        x_tenant_id (str):
        body (EdgeCalibrationReportRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeCalibrationReport]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeCalibrationReportRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeCalibrationReport]]:
    """Generate an Edge calibration report

     Summarizes the policy decisions recorded on the Edge events of the
    tenant's observe-mode sessions in the window [since, until), narrowed
    to the given repositories and labels when set. The window may not
    exceed 90 days. The report is stored under its content-derived id and
    returned. Requires policy.write or the admin role.

    Args:
        x_tenant_id (str):
        body (EdgeCalibrationReportRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeCalibrationReport]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
