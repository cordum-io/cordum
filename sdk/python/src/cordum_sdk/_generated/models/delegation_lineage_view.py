from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.delegation_lineage_chain_link import DelegationLineageChainLink


T = TypeVar("T", bound="DelegationLineageView")


@_attrs_define
class DelegationLineageView:
    """
    Attributes:
        jti (Union[Unset, str]):
        audience (Union[Unset, str]):
        root_issuer (Union[Unset, str]):
        parent_issuer (Union[Unset, str]):
        chain_depth (Union[Unset, int]):
        chain (Union[Unset, List['DelegationLineageChainLink']]):
        scope (Union[Unset, List[str]]):
        expires_at (Union[Unset, datetime.datetime]):
        verified_at (Union[Unset, int]):
        reverified_at_dispatch (Union[Unset, bool]):
    """

    jti: Union[Unset, str] = UNSET
    audience: Union[Unset, str] = UNSET
    root_issuer: Union[Unset, str] = UNSET
    parent_issuer: Union[Unset, str] = UNSET
    chain_depth: Union[Unset, int] = UNSET
    chain: Union[Unset, List["DelegationLineageChainLink"]] = UNSET
    scope: Union[Unset, List[str]] = UNSET
    expires_at: Union[Unset, datetime.datetime] = UNSET
    verified_at: Union[Unset, int] = UNSET
    reverified_at_dispatch: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.delegation_lineage_chain_link import DelegationLineageChainLink

        jti = self.jti

        audience = self.audience

        root_issuer = self.root_issuer

        parent_issuer = self.parent_issuer

        chain_depth = self.chain_depth

        chain: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.chain, Unset):
            chain = []
            for chain_item_data in self.chain:
                chain_item = chain_item_data.to_dict()
                chain.append(chain_item)

        scope: Union[Unset, List[str]] = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope

        expires_at: Union[Unset, str] = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        verified_at = self.verified_at

        reverified_at_dispatch = self.reverified_at_dispatch

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if jti is not UNSET:
            field_dict["jti"] = jti
        if audience is not UNSET:
            field_dict["audience"] = audience
        if root_issuer is not UNSET:
            field_dict["root_issuer"] = root_issuer
        if parent_issuer is not UNSET:
            field_dict["parent_issuer"] = parent_issuer
        if chain_depth is not UNSET:
            field_dict["chain_depth"] = chain_depth
        if chain is not UNSET:
            field_dict["chain"] = chain
        if scope is not UNSET:
            field_dict["scope"] = scope
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if reverified_at_dispatch is not UNSET:
            field_dict["reverified_at_dispatch"] = reverified_at_dispatch

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.delegation_lineage_chain_link import DelegationLineageChainLink

        d = src_dict.copy()
        jti = d.pop("jti", UNSET)

        audience = d.pop("audience", UNSET)

        root_issuer = d.pop("root_issuer", UNSET)

        parent_issuer = d.pop("parent_issuer", UNSET)

        chain_depth = d.pop("chain_depth", UNSET)

        chain = []
        _chain = d.pop("chain", UNSET)
        for chain_item_data in _chain or []:
            chain_item = DelegationLineageChainLink.from_dict(chain_item_data)

            chain.append(chain_item)

        scope = cast(List[str], d.pop("scope", UNSET))

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: Union[Unset, datetime.datetime]
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = isoparse(_expires_at)

        verified_at = d.pop("verified_at", UNSET)

        reverified_at_dispatch = d.pop("reverified_at_dispatch", UNSET)

        delegation_lineage_view = cls(
            jti=jti,
            audience=audience,
            root_issuer=root_issuer,
            parent_issuer=parent_issuer,
            chain_depth=chain_depth,
            chain=chain,
            scope=scope,
            expires_at=expires_at,
            verified_at=verified_at,
            reverified_at_dispatch=reverified_at_dispatch,
        )

        delegation_lineage_view.additional_properties = d
        return delegation_lineage_view

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
