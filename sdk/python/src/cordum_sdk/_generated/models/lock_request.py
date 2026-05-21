from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.lock_request_mode import LockRequestMode
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="LockRequest")


@_attrs_define
class LockRequest:
    """
    Attributes:
        resource (str):
        owner (str):
        mode (Union[Unset, LockRequestMode]):
        ttl_ms (Union[Unset, int]): Lock time-to-live in milliseconds
    """

    resource: str
    owner: str
    mode: Union[Unset, LockRequestMode] = UNSET
    ttl_ms: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        resource = self.resource

        owner = self.owner

        mode: Union[Unset, str] = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode.value

        ttl_ms = self.ttl_ms

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resource": resource,
                "owner": owner,
            }
        )
        if mode is not UNSET:
            field_dict["mode"] = mode
        if ttl_ms is not UNSET:
            field_dict["ttl_ms"] = ttl_ms

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        resource = d.pop("resource")

        owner = d.pop("owner")

        _mode = d.pop("mode", UNSET)
        mode: Union[Unset, LockRequestMode]
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = LockRequestMode(_mode)

        ttl_ms = d.pop("ttl_ms", UNSET)

        lock_request = cls(
            resource=resource,
            owner=owner,
            mode=mode,
            ttl_ms=ttl_ms,
        )

        lock_request.additional_properties = d
        return lock_request

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
