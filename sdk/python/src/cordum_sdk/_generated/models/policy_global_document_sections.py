from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import Dict

if TYPE_CHECKING:
    from ..models.policy_global_section import PolicyGlobalSection


T = TypeVar("T", bound="PolicyGlobalDocumentSections")


@_attrs_define
class PolicyGlobalDocumentSections:
    """Keyed by section name — input_rules, output_rules, edge_action_rules, mcp_tool_rules, invariants."""

    additional_properties: Dict[str, "PolicyGlobalSection"] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_global_section import PolicyGlobalSection

        field_dict: Dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_global_section import PolicyGlobalSection

        d = src_dict.copy()
        policy_global_document_sections = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = PolicyGlobalSection.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        policy_global_document_sections.additional_properties = additional_properties
        return policy_global_document_sections

    @property
    def additional_keys(self) -> List[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> "PolicyGlobalSection":
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: "PolicyGlobalSection") -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
