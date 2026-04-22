from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union






T = TypeVar("T", bound="IssueDelegationTokenResponse201")


@_attrs_define
class IssueDelegationTokenResponse201:
    """ 
        Attributes:
            token (Union[Unset, str]):
            kid (Union[Unset, str]):
            expires_at (Union[Unset, str]):
            chain_depth (Union[Unset, int]):
            jti (Union[Unset, str]):
     """

    token: Union[Unset, str] = UNSET
    kid: Union[Unset, str] = UNSET
    expires_at: Union[Unset, str] = UNSET
    chain_depth: Union[Unset, int] = UNSET
    jti: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        token = self.token

        kid = self.kid

        expires_at = self.expires_at

        chain_depth = self.chain_depth

        jti = self.jti


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if token is not UNSET:
            field_dict["token"] = token
        if kid is not UNSET:
            field_dict["kid"] = kid
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if chain_depth is not UNSET:
            field_dict["chain_depth"] = chain_depth
        if jti is not UNSET:
            field_dict["jti"] = jti

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        token = d.pop("token", UNSET)

        kid = d.pop("kid", UNSET)

        expires_at = d.pop("expires_at", UNSET)

        chain_depth = d.pop("chain_depth", UNSET)

        jti = d.pop("jti", UNSET)

        issue_delegation_token_response_201 = cls(
            token=token,
            kid=kid,
            expires_at=expires_at,
            chain_depth=chain_depth,
            jti=jti,
        )


        issue_delegation_token_response_201.additional_properties = d
        return issue_delegation_token_response_201

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
