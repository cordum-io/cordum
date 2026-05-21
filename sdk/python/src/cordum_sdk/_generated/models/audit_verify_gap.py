from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.audit_verify_gap_type import AuditVerifyGapType


T = TypeVar("T", bound="AuditVerifyGap")


@_attrs_define
class AuditVerifyGap:
    """
    Attributes:
        at_seq (int): Sequence number where the gap or mismatch was observed.
        type (AuditVerifyGapType):
    """

    at_seq: int
    type: AuditVerifyGapType
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        at_seq = self.at_seq

        type = self.type.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at_seq": at_seq,
                "type": type,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        at_seq = d.pop("at_seq")

        type = AuditVerifyGapType(d.pop("type"))

        audit_verify_gap = cls(
            at_seq=at_seq,
            type=type,
        )

        audit_verify_gap.additional_properties = d
        return audit_verify_gap

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
