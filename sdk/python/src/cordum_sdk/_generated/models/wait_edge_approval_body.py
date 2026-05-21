from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="WaitEdgeApprovalBody")


@_attrs_define
class WaitEdgeApprovalBody:
    """
    Attributes:
        timeout_ms (Union[Unset, int]): Caller-requested wait budget. Server clamps to a 5-minute max and uses a 30s
            default when omitted or non-positive.
    """

    timeout_ms: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        timeout_ms = self.timeout_ms

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if timeout_ms is not UNSET:
            field_dict["timeout_ms"] = timeout_ms

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        timeout_ms = d.pop("timeout_ms", UNSET)

        wait_edge_approval_body = cls(
            timeout_ms=timeout_ms,
        )

        wait_edge_approval_body.additional_properties = d
        return wait_edge_approval_body

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
