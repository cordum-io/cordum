from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="ShadowRemediationAPIRequest")


@_attrs_define
class ShadowRemediationAPIRequest:
    """
    Attributes:
        method (str):
        path (str):
        body (Union[Unset, str]): Stripped when omit_commands=true.
    """

    method: str
    path: str
    body: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        method = self.method

        path = self.path

        body = self.body

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
            }
        )
        if body is not UNSET:
            field_dict["body"] = body

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        method = d.pop("method")

        path = d.pop("path")

        body = d.pop("body", UNSET)

        shadow_remediation_api_request = cls(
            method=method,
            path=path,
            body=body,
        )

        shadow_remediation_api_request.additional_properties = d
        return shadow_remediation_api_request

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
