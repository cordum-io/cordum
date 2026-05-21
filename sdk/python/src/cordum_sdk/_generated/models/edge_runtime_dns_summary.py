from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EdgeRuntimeDNSSummary")


@_attrs_define
class EdgeRuntimeDNSSummary:
    """
    Attributes:
        qname_redacted (Union[Unset, str]):
        qtype (Union[Unset, str]):
    """

    qname_redacted: Union[Unset, str] = UNSET
    qtype: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        qname_redacted = self.qname_redacted

        qtype = self.qtype

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if qname_redacted is not UNSET:
            field_dict["qname_redacted"] = qname_redacted
        if qtype is not UNSET:
            field_dict["qtype"] = qtype

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        qname_redacted = d.pop("qname_redacted", UNSET)

        qtype = d.pop("qtype", UNSET)

        edge_runtime_dns_summary = cls(
            qname_redacted=qname_redacted,
            qtype=qtype,
        )

        edge_runtime_dns_summary.additional_properties = d
        return edge_runtime_dns_summary

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
