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


T = TypeVar("T", bound="AuthUser")


@_attrs_define
class AuthUser:
    """
    Attributes:
        id (Union[Unset, str]):
        username (Union[Unset, str]):
        email (Union[Unset, str]):
        display_name (Union[Unset, str]):
        tenant (Union[Unset, str]):
        roles (Union[Unset, List[str]]):
        source (Union[Unset, str]): Auth source (local, saml, oidc)
        created_at (Union[Unset, datetime.datetime]):
        updated_at (Union[Unset, datetime.datetime]):
        last_login_at (Union[None, Unset, datetime.datetime]):
    """

    id: Union[Unset, str] = UNSET
    username: Union[Unset, str] = UNSET
    email: Union[Unset, str] = UNSET
    display_name: Union[Unset, str] = UNSET
    tenant: Union[Unset, str] = UNSET
    roles: Union[Unset, List[str]] = UNSET
    source: Union[Unset, str] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    last_login_at: Union[None, Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        username = self.username

        email = self.email

        display_name = self.display_name

        tenant = self.tenant

        roles: Union[Unset, List[str]] = UNSET
        if not isinstance(self.roles, Unset):
            roles = self.roles

        source = self.source

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        last_login_at: Union[None, Unset, str]
        if isinstance(self.last_login_at, Unset):
            last_login_at = UNSET
        elif isinstance(self.last_login_at, datetime.datetime):
            last_login_at = self.last_login_at.isoformat()
        else:
            last_login_at = self.last_login_at

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if username is not UNSET:
            field_dict["username"] = username
        if email is not UNSET:
            field_dict["email"] = email
        if display_name is not UNSET:
            field_dict["display_name"] = display_name
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if roles is not UNSET:
            field_dict["roles"] = roles
        if source is not UNSET:
            field_dict["source"] = source
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if last_login_at is not UNSET:
            field_dict["last_login_at"] = last_login_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        username = d.pop("username", UNSET)

        email = d.pop("email", UNSET)

        display_name = d.pop("display_name", UNSET)

        tenant = d.pop("tenant", UNSET)

        roles = cast(List[str], d.pop("roles", UNSET))

        source = d.pop("source", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        def _parse_last_login_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_login_at_type_0 = isoparse(data)

                return last_login_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        last_login_at = _parse_last_login_at(d.pop("last_login_at", UNSET))

        auth_user = cls(
            id=id,
            username=username,
            email=email,
            display_name=display_name,
            tenant=tenant,
            roles=roles,
            source=source,
            created_at=created_at,
            updated_at=updated_at,
            last_login_at=last_login_at,
        )

        auth_user.additional_properties = d
        return auth_user

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
