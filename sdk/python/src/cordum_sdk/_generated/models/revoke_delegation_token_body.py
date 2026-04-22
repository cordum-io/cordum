from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union






T = TypeVar("T", bound="RevokeDelegationTokenBody")


@_attrs_define
class RevokeDelegationTokenBody:
    """ 
        Attributes:
            jti (str):
            reason (Union[Unset, str]):
            cascade (Union[Unset, bool]): When true, revoke downstream delegations that extended this token. Default: True.
     """

    jti: str
    reason: Union[Unset, str] = UNSET
    cascade: Union[Unset, bool] = True
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        jti = self.jti

        reason = self.reason

        cascade = self.cascade


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "jti": jti,
        })
        if reason is not UNSET:
            field_dict["reason"] = reason
        if cascade is not UNSET:
            field_dict["cascade"] = cascade

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        jti = d.pop("jti")

        reason = d.pop("reason", UNSET)

        cascade = d.pop("cascade", UNSET)

        revoke_delegation_token_body = cls(
            jti=jti,
            reason=reason,
            cascade=cascade,
        )


        revoke_delegation_token_body.additional_properties = d
        return revoke_delegation_token_body

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
