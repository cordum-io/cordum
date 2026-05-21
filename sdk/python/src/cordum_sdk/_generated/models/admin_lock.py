from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset


T = TypeVar("T", bound="AdminLock")


@_attrs_define
class AdminLock:
    """
    Attributes:
        key (str):
        holder (str):
        ttl_remaining_ms (int):
        type (str):
    """

    key: str
    holder: str
    ttl_remaining_ms: int
    type: str
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        key = self.key

        holder = self.holder

        ttl_remaining_ms = self.ttl_remaining_ms

        type = self.type

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "holder": holder,
                "ttl_remaining_ms": ttl_remaining_ms,
                "type": type,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        key = d.pop("key")

        holder = d.pop("holder")

        ttl_remaining_ms = d.pop("ttl_remaining_ms")

        type = d.pop("type")

        admin_lock = cls(
            key=key,
            holder=holder,
            ttl_remaining_ms=ttl_remaining_ms,
            type=type,
        )

        admin_lock.additional_properties = d
        return admin_lock

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
