from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Union
import datetime






T = TypeVar("T", bound="DelegationChainLink")


@_attrs_define
class DelegationChainLink:
    """ 
        Attributes:
            agent_id (str):
            issued_at (datetime.datetime):
            expires_at (datetime.datetime):
            jti (str):
            issued_by (str):
            parent_jti (Union[None, Unset, str]):
     """

    agent_id: str
    issued_at: datetime.datetime
    expires_at: datetime.datetime
    jti: str
    issued_by: str
    parent_jti: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        agent_id = self.agent_id

        issued_at = self.issued_at.isoformat()

        expires_at = self.expires_at.isoformat()

        jti = self.jti

        issued_by = self.issued_by

        parent_jti: Union[None, Unset, str]
        if isinstance(self.parent_jti, Unset):
            parent_jti = UNSET
        else:
            parent_jti = self.parent_jti


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "agent_id": agent_id,
            "issued_at": issued_at,
            "expires_at": expires_at,
            "jti": jti,
            "issued_by": issued_by,
        })
        if parent_jti is not UNSET:
            field_dict["parent_jti"] = parent_jti

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        agent_id = d.pop("agent_id")

        issued_at = isoparse(d.pop("issued_at"))




        expires_at = isoparse(d.pop("expires_at"))




        jti = d.pop("jti")

        issued_by = d.pop("issued_by")

        def _parse_parent_jti(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        parent_jti = _parse_parent_jti(d.pop("parent_jti", UNSET))


        delegation_chain_link = cls(
            agent_id=agent_id,
            issued_at=issued_at,
            expires_at=expires_at,
            jti=jti,
            issued_by=issued_by,
            parent_jti=parent_jti,
        )


        delegation_chain_link.additional_properties = d
        return delegation_chain_link

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
