from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.lock_mode import LockMode
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="Lock")


@_attrs_define
class Lock:
    """
    Attributes:
        resource (Union[Unset, str]):
        mode (Union[Unset, LockMode]):
        owners (Union[Unset, List[str]]):
        updated_at (Union[Unset, datetime.datetime]):
        expires_at (Union[Unset, datetime.datetime]):
    """

    resource: Union[Unset, str] = UNSET
    mode: Union[Unset, LockMode] = UNSET
    owners: Union[Unset, List[str]] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    expires_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        resource = self.resource

        mode: Union[Unset, str] = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode.value

        owners: Union[Unset, List[str]] = UNSET
        if not isinstance(self.owners, Unset):
            owners = self.owners

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        expires_at: Union[Unset, str] = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if resource is not UNSET:
            field_dict["resource"] = resource
        if mode is not UNSET:
            field_dict["mode"] = mode
        if owners is not UNSET:
            field_dict["owners"] = owners
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        resource = d.pop("resource", UNSET)

        _mode = d.pop("mode", UNSET)
        mode: Union[Unset, LockMode]
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = LockMode(_mode)

        owners = cast(List[str], d.pop("owners", UNSET))

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: Union[Unset, datetime.datetime]
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = isoparse(_expires_at)

        lock = cls(
            resource=resource,
            mode=mode,
            owners=owners,
            updated_at=updated_at,
            expires_at=expires_at,
        )

        lock.additional_properties = d
        return lock

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
