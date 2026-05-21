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


T = TypeVar("T", bound="CreateAPIKeyRequest")


@_attrs_define
class CreateAPIKeyRequest:
    """
    Attributes:
        name (str):
        scopes (Union[Unset, List[str]]):
        expires_at (Union[None, Unset, datetime.datetime]):
    """

    name: str
    scopes: Union[Unset, List[str]] = UNSET
    expires_at: Union[None, Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        scopes: Union[Unset, List[str]] = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = self.scopes

        expires_at: Union[None, Unset, str]
        if isinstance(self.expires_at, Unset):
            expires_at = UNSET
        elif isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if scopes is not UNSET:
            field_dict["scopes"] = scopes
        if expires_at is not UNSET:
            field_dict["expiresAt"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name")

        scopes = cast(List[str], d.pop("scopes", UNSET))

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

        create_api_key_request = cls(
            name=name,
            scopes=scopes,
            expires_at=expires_at,
        )

        create_api_key_request.additional_properties = d
        return create_api_key_request

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
