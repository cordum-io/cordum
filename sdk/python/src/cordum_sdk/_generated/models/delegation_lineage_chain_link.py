from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="DelegationLineageChainLink")


@_attrs_define
class DelegationLineageChainLink:
    """
    Attributes:
        agent_id (Union[Unset, str]):
        issued_at (Union[Unset, datetime.datetime]):
        expires_at (Union[Unset, datetime.datetime]):
        jti (Union[Unset, str]):
        parent_jti (Union[Unset, str]):
    """

    agent_id: Union[Unset, str] = UNSET
    issued_at: Union[Unset, datetime.datetime] = UNSET
    expires_at: Union[Unset, datetime.datetime] = UNSET
    jti: Union[Unset, str] = UNSET
    parent_jti: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        agent_id = self.agent_id

        issued_at: Union[Unset, str] = UNSET
        if not isinstance(self.issued_at, Unset):
            issued_at = self.issued_at.isoformat()

        expires_at: Union[Unset, str] = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        jti = self.jti

        parent_jti = self.parent_jti

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if issued_at is not UNSET:
            field_dict["issued_at"] = issued_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if jti is not UNSET:
            field_dict["jti"] = jti
        if parent_jti is not UNSET:
            field_dict["parent_jti"] = parent_jti

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        agent_id = d.pop("agent_id", UNSET)

        _issued_at = d.pop("issued_at", UNSET)
        issued_at: Union[Unset, datetime.datetime]
        if isinstance(_issued_at, Unset):
            issued_at = UNSET
        else:
            issued_at = isoparse(_issued_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: Union[Unset, datetime.datetime]
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = isoparse(_expires_at)

        jti = d.pop("jti", UNSET)

        parent_jti = d.pop("parent_jti", UNSET)

        delegation_lineage_chain_link = cls(
            agent_id=agent_id,
            issued_at=issued_at,
            expires_at=expires_at,
            jti=jti,
            parent_jti=parent_jti,
        )

        delegation_lineage_chain_link.additional_properties = d
        return delegation_lineage_chain_link

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
