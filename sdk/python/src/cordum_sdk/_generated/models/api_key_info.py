from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Union
import datetime


T = TypeVar("T", bound="APIKeyInfo")


@_attrs_define
class APIKeyInfo:
    """
    Attributes:
        id (Union[Unset, str]):
        name (Union[Unset, str]):
        prefix (Union[Unset, str]): First characters of the key for identification
        scopes (Union[Unset, List[str]]):
        created_at (Union[Unset, datetime.datetime]):
        last_used (Union[None, Unset, datetime.datetime]):
        usage_count (Union[Unset, int]):
        expires_at (Union[None, Unset, datetime.datetime]):
    """

    id: Union[Unset, str] = UNSET
    name: Union[Unset, str] = UNSET
    prefix: Union[Unset, str] = UNSET
    scopes: Union[Unset, List[str]] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    last_used: Union[None, Unset, datetime.datetime] = UNSET
    usage_count: Union[Unset, int] = UNSET
    expires_at: Union[None, Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        name = self.name

        prefix = self.prefix

        scopes: Union[Unset, List[str]] = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = self.scopes

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        last_used: Union[None, Unset, str]
        if isinstance(self.last_used, Unset):
            last_used = UNSET
        elif isinstance(self.last_used, datetime.datetime):
            last_used = self.last_used.isoformat()
        else:
            last_used = self.last_used

        usage_count = self.usage_count

        expires_at: Union[None, Unset, str]
        if isinstance(self.expires_at, Unset):
            expires_at = UNSET
        elif isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if name is not UNSET:
            field_dict["name"] = name
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if scopes is not UNSET:
            field_dict["scopes"] = scopes
        if created_at is not UNSET:
            field_dict["createdAt"] = created_at
        if last_used is not UNSET:
            field_dict["lastUsed"] = last_used
        if usage_count is not UNSET:
            field_dict["usageCount"] = usage_count
        if expires_at is not UNSET:
            field_dict["expiresAt"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        name = d.pop("name", UNSET)

        prefix = d.pop("prefix", UNSET)

        scopes = cast(List[str], d.pop("scopes", UNSET))

        _created_at = d.pop("createdAt", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        def _parse_last_used(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_used_type_0 = isoparse(data)

                return last_used_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        last_used = _parse_last_used(d.pop("lastUsed", UNSET))

        usage_count = d.pop("usageCount", UNSET)

        def _parse_expires_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                expires_at_type_0 = isoparse(data)

                return expires_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        expires_at = _parse_expires_at(d.pop("expiresAt", UNSET))

        api_key_info = cls(
            id=id,
            name=name,
            prefix=prefix,
            scopes=scopes,
            created_at=created_at,
            last_used=last_used,
            usage_count=usage_count,
            expires_at=expires_at,
        )

        api_key_info.additional_properties = d
        return api_key_info

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
