from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="AdoptRetentionAnchorRequest")


@_attrs_define
class AdoptRetentionAnchorRequest:
    """
    Attributes:
        expected_oldest_seq (int): Seq of the oldest retained event, as verify reports it (`first_seq`). Adoption is
            refused when the chain starts anywhere else.
        tenant_id (Union[Unset, str]): Tenant whose chain to anchor; must match caller scope and defaults to the
            authenticated/request tenant.
        replace_invalid (Union[Unset, bool]): Replace an existing anchor that does not anchor the chain (malformed,
            `hmac_mismatch`, or not linked to the oldest retained event). A valid anchor is never replaced. Default:
            False.
    """

    expected_oldest_seq: int
    tenant_id: Union[Unset, str] = UNSET
    replace_invalid: Union[Unset, bool] = False
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        expected_oldest_seq = self.expected_oldest_seq

        tenant_id = self.tenant_id

        replace_invalid = self.replace_invalid

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_oldest_seq": expected_oldest_seq,
            }
        )
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if replace_invalid is not UNSET:
            field_dict["replace_invalid"] = replace_invalid

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        expected_oldest_seq = d.pop("expected_oldest_seq")

        tenant_id = d.pop("tenant_id", UNSET)

        replace_invalid = d.pop("replace_invalid", UNSET)

        adopt_retention_anchor_request = cls(
            expected_oldest_seq=expected_oldest_seq,
            tenant_id=tenant_id,
            replace_invalid=replace_invalid,
        )

        adopt_retention_anchor_request.additional_properties = d
        return adopt_retention_anchor_request

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
