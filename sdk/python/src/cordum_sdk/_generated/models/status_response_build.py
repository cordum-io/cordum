from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="StatusResponseBuild")


@_attrs_define
class StatusResponseBuild:
    """
    Attributes:
        version (Union[Unset, str]):
        commit (Union[Unset, str]):
        date (Union[Unset, str]):
    """

    version: Union[Unset, str] = UNSET
    commit: Union[Unset, str] = UNSET
    date: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        version = self.version

        commit = self.commit

        date = self.date

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if version is not UNSET:
            field_dict["version"] = version
        if commit is not UNSET:
            field_dict["commit"] = commit
        if date is not UNSET:
            field_dict["date"] = date

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        version = d.pop("version", UNSET)

        commit = d.pop("commit", UNSET)

        date = d.pop("date", UNSET)

        status_response_build = cls(
            version=version,
            commit=commit,
            date=date,
        )

        status_response_build.additional_properties = d
        return status_response_build

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
