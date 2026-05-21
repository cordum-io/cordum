from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.auth_user import AuthUser


T = TypeVar("T", bound="LoginResponse")


@_attrs_define
class LoginResponse:
    """
    Attributes:
        token (Union[Unset, str]):
        expires_at (Union[Unset, datetime.datetime]):
        user (Union[Unset, AuthUser]):
    """

    token: Union[Unset, str] = UNSET
    expires_at: Union[Unset, datetime.datetime] = UNSET
    user: Union[Unset, "AuthUser"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.auth_user import AuthUser

        token = self.token

        expires_at: Union[Unset, str] = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        user: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.user, Unset):
            user = self.user.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if token is not UNSET:
            field_dict["token"] = token
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if user is not UNSET:
            field_dict["user"] = user

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.auth_user import AuthUser

        d = src_dict.copy()
        token = d.pop("token", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: Union[Unset, datetime.datetime]
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = isoparse(_expires_at)

        _user = d.pop("user", UNSET)
        user: Union[Unset, AuthUser]
        if isinstance(_user, Unset):
            user = UNSET
        else:
            user = AuthUser.from_dict(_user)

        login_response = cls(
            token=token,
            expires_at=expires_at,
            user=user,
        )

        login_response.additional_properties = d
        return login_response

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
