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
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
  from ..models.delegation_chain_link import DelegationChainLink





T = TypeVar("T", bound="DelegationView")


@_attrs_define
class DelegationView:
    """ 
        Attributes:
            jti (str):
            issuer (str): Root issuer for the delegation chain.
            subject (str): Agent that issued the token.
            audience (str): Target agent the token was minted for.
            chain_depth (int):
            issued_at (datetime.datetime):
            expires_at (datetime.datetime):
            revoked (bool):
            allowed_actions (Union[Unset, List[str]]):
            allowed_topics (Union[Unset, List[str]]):
            chain (Union[Unset, List['DelegationChainLink']]):
            revoked_at (Union[None, Unset, datetime.datetime]):
            revoked_reason (Union[None, Unset, str]):
     """

    jti: str
    issuer: str
    subject: str
    audience: str
    chain_depth: int
    issued_at: datetime.datetime
    expires_at: datetime.datetime
    revoked: bool
    allowed_actions: Union[Unset, List[str]] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    chain: Union[Unset, List['DelegationChainLink']] = UNSET
    revoked_at: Union[None, Unset, datetime.datetime] = UNSET
    revoked_reason: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.delegation_chain_link import DelegationChainLink
        jti = self.jti

        issuer = self.issuer

        subject = self.subject

        audience = self.audience

        chain_depth = self.chain_depth

        issued_at = self.issued_at.isoformat()

        expires_at = self.expires_at.isoformat()

        revoked = self.revoked

        allowed_actions: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_actions, Unset):
            allowed_actions = self.allowed_actions



        allowed_topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_topics, Unset):
            allowed_topics = self.allowed_topics



        chain: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.chain, Unset):
            chain = []
            for chain_item_data in self.chain:
                chain_item = chain_item_data.to_dict()
                chain.append(chain_item)



        revoked_at: Union[None, Unset, str]
        if isinstance(self.revoked_at, Unset):
            revoked_at = UNSET
        elif isinstance(self.revoked_at, datetime.datetime):
            revoked_at = self.revoked_at.isoformat()
        else:
            revoked_at = self.revoked_at

        revoked_reason: Union[None, Unset, str]
        if isinstance(self.revoked_reason, Unset):
            revoked_reason = UNSET
        else:
            revoked_reason = self.revoked_reason


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "jti": jti,
            "issuer": issuer,
            "subject": subject,
            "audience": audience,
            "chain_depth": chain_depth,
            "issued_at": issued_at,
            "expires_at": expires_at,
            "revoked": revoked,
        })
        if allowed_actions is not UNSET:
            field_dict["allowed_actions"] = allowed_actions
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if chain is not UNSET:
            field_dict["chain"] = chain
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at
        if revoked_reason is not UNSET:
            field_dict["revoked_reason"] = revoked_reason

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.delegation_chain_link import DelegationChainLink
        d = src_dict.copy()
        jti = d.pop("jti")

        issuer = d.pop("issuer")

        subject = d.pop("subject")

        audience = d.pop("audience")

        chain_depth = d.pop("chain_depth")

        issued_at = isoparse(d.pop("issued_at"))




        expires_at = isoparse(d.pop("expires_at"))




        revoked = d.pop("revoked")

        allowed_actions = cast(List[str], d.pop("allowed_actions", UNSET))


        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))


        chain = []
        _chain = d.pop("chain", UNSET)
        for chain_item_data in (_chain or []):
            chain_item = DelegationChainLink.from_dict(chain_item_data)



            chain.append(chain_item)


        def _parse_revoked_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                revoked_at_type_0 = isoparse(data)



                return revoked_at_type_0
            except: # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        revoked_at = _parse_revoked_at(d.pop("revoked_at", UNSET))


        def _parse_revoked_reason(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        revoked_reason = _parse_revoked_reason(d.pop("revoked_reason", UNSET))


        delegation_view = cls(
            jti=jti,
            issuer=issuer,
            subject=subject,
            audience=audience,
            chain_depth=chain_depth,
            issued_at=issued_at,
            expires_at=expires_at,
            revoked=revoked,
            allowed_actions=allowed_actions,
            allowed_topics=allowed_topics,
            chain=chain,
            revoked_at=revoked_at,
            revoked_reason=revoked_reason,
        )


        delegation_view.additional_properties = d
        return delegation_view

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
