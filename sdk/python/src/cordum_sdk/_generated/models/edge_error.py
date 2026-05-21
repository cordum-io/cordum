from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_error_code import EdgeErrorCode
from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.edge_error_details import EdgeErrorDetails


T = TypeVar("T", bound="EdgeError")


@_attrs_define
class EdgeError:
    """Standard error envelope for all `/api/v1/edge/*` routes. Messages and details are sanitized and must not echo raw
    tool payloads, API keys, signed URLs, or secrets.

        Attributes:
            code (EdgeErrorCode): Stable machine-readable Edge error code. Example: idempotency_conflict.
            message (str): Sanitized human-readable message. Example: idempotency key already used with a different request.
            request_id (str): Request correlation id from `X-Request-Id`/middleware.
            details (Union[Unset, EdgeErrorDetails]): Optional sanitized structured details.
    """

    code: EdgeErrorCode
    message: str
    request_id: str
    details: Union[Unset, "EdgeErrorDetails"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_error_details import EdgeErrorDetails

        code = self.code.value

        message = self.message

        request_id = self.request_id

        details: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.details, Unset):
            details = self.details.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "message": message,
                "request_id": request_id,
            }
        )
        if details is not UNSET:
            field_dict["details"] = details

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_error_details import EdgeErrorDetails

        d = src_dict.copy()
        code = EdgeErrorCode(d.pop("code"))

        message = d.pop("message")

        request_id = d.pop("request_id")

        _details = d.pop("details", UNSET)
        details: Union[Unset, EdgeErrorDetails]
        if isinstance(_details, Unset):
            details = UNSET
        else:
            details = EdgeErrorDetails.from_dict(_details)

        edge_error = cls(
            code=code,
            message=message,
            request_id=request_id,
            details=details,
        )

        edge_error.additional_properties = d
        return edge_error

    @property
    def additional_keys(self) -> List[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
