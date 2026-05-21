from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.delegation_chain_link import DelegationChainLink


T = TypeVar("T", bound="VerifyDelegationTokenResponse200")


@_attrs_define
class VerifyDelegationTokenResponse200:
    """
    Attributes:
        valid (Union[Unset, bool]):
        sub (Union[Unset, str]):
        aud (Union[Unset, str]):
        allowed_actions (Union[Unset, List[str]]):
        allowed_topics (Union[Unset, List[str]]):
        chain_depth (Union[Unset, int]):
        delegation_chain (Union[Unset, List['DelegationChainLink']]):
        error_code (Union[Unset, str]):
    """

    valid: Union[Unset, bool] = UNSET
    sub: Union[Unset, str] = UNSET
    aud: Union[Unset, str] = UNSET
    allowed_actions: Union[Unset, List[str]] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    chain_depth: Union[Unset, int] = UNSET
    delegation_chain: Union[Unset, List["DelegationChainLink"]] = UNSET
    error_code: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.delegation_chain_link import DelegationChainLink

        valid = self.valid

        sub = self.sub

        aud = self.aud

        allowed_actions: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_actions, Unset):
            allowed_actions = self.allowed_actions

        allowed_topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_topics, Unset):
            allowed_topics = self.allowed_topics

        chain_depth = self.chain_depth

        delegation_chain: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.delegation_chain, Unset):
            delegation_chain = []
            for delegation_chain_item_data in self.delegation_chain:
                delegation_chain_item = delegation_chain_item_data.to_dict()
                delegation_chain.append(delegation_chain_item)

        error_code = self.error_code

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if valid is not UNSET:
            field_dict["valid"] = valid
        if sub is not UNSET:
            field_dict["sub"] = sub
        if aud is not UNSET:
            field_dict["aud"] = aud
        if allowed_actions is not UNSET:
            field_dict["allowed_actions"] = allowed_actions
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if chain_depth is not UNSET:
            field_dict["chain_depth"] = chain_depth
        if delegation_chain is not UNSET:
            field_dict["delegation_chain"] = delegation_chain
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.delegation_chain_link import DelegationChainLink

        d = src_dict.copy()
        valid = d.pop("valid", UNSET)

        sub = d.pop("sub", UNSET)

        aud = d.pop("aud", UNSET)

        allowed_actions = cast(List[str], d.pop("allowed_actions", UNSET))

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))

        chain_depth = d.pop("chain_depth", UNSET)

        delegation_chain = []
        _delegation_chain = d.pop("delegation_chain", UNSET)
        for delegation_chain_item_data in _delegation_chain or []:
            delegation_chain_item = DelegationChainLink.from_dict(delegation_chain_item_data)

            delegation_chain.append(delegation_chain_item)

        error_code = d.pop("error_code", UNSET)

        verify_delegation_token_response_200 = cls(
            valid=valid,
            sub=sub,
            aud=aud,
            allowed_actions=allowed_actions,
            allowed_topics=allowed_topics,
            chain_depth=chain_depth,
            delegation_chain=delegation_chain,
            error_code=error_code,
        )

        verify_delegation_token_response_200.additional_properties = d
        return verify_delegation_token_response_200

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
