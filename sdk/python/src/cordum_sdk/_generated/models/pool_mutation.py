from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="PoolMutation")


@_attrs_define
class PoolMutation:
    """
    Attributes:
        name (str):
        status (str):
        requires (Union[Unset, List[str]]):
        description (Union[Unset, str]):
        drain_started_at (Union[Unset, datetime.datetime]):
        drain_timeout_seconds (Union[Unset, int]):
    """

    name: str
    status: str
    requires: Union[Unset, List[str]] = UNSET
    description: Union[Unset, str] = UNSET
    drain_started_at: Union[Unset, datetime.datetime] = UNSET
    drain_timeout_seconds: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        status = self.status

        requires: Union[Unset, List[str]] = UNSET
        if not isinstance(self.requires, Unset):
            requires = self.requires

        description = self.description

        drain_started_at: Union[Unset, str] = UNSET
        if not isinstance(self.drain_started_at, Unset):
            drain_started_at = self.drain_started_at.isoformat()

        drain_timeout_seconds = self.drain_timeout_seconds

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "status": status,
            }
        )
        if requires is not UNSET:
            field_dict["requires"] = requires
        if description is not UNSET:
            field_dict["description"] = description
        if drain_started_at is not UNSET:
            field_dict["drain_started_at"] = drain_started_at
        if drain_timeout_seconds is not UNSET:
            field_dict["drain_timeout_seconds"] = drain_timeout_seconds

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name")

        status = d.pop("status")

        requires = cast(List[str], d.pop("requires", UNSET))

        description = d.pop("description", UNSET)

        _drain_started_at = d.pop("drain_started_at", UNSET)
        drain_started_at: Union[Unset, datetime.datetime]
        if isinstance(_drain_started_at, Unset):
            drain_started_at = UNSET
        else:
            drain_started_at = isoparse(_drain_started_at)

        drain_timeout_seconds = d.pop("drain_timeout_seconds", UNSET)

        pool_mutation = cls(
            name=name,
            status=status,
            requires=requires,
            description=description,
            drain_started_at=drain_started_at,
            drain_timeout_seconds=drain_timeout_seconds,
        )

        pool_mutation.additional_properties = d
        return pool_mutation

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
