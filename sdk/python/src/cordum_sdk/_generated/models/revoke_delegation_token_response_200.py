from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset


T = TypeVar("T", bound="RevokeDelegationTokenResponse200")


@_attrs_define
class RevokeDelegationTokenResponse200:
    """
    Attributes:
        jti (str):
        cascaded_count (int): Number of downstream delegations revoked in addition to the root token.
    """

    jti: str
    cascaded_count: int
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        jti = self.jti

        cascaded_count = self.cascaded_count

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "jti": jti,
                "cascaded_count": cascaded_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        jti = d.pop("jti")

        cascaded_count = d.pop("cascaded_count")

        revoke_delegation_token_response_200 = cls(
            jti=jti,
            cascaded_count=cascaded_count,
        )

        revoke_delegation_token_response_200.additional_properties = d
        return revoke_delegation_token_response_200

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
