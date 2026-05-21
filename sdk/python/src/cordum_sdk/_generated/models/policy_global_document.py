from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.policy_global_document_sections import PolicyGlobalDocumentSections


T = TypeVar("T", bound="PolicyGlobalDocument")


@_attrs_define
class PolicyGlobalDocument:
    """EDGE-052 — five-section view of the Global policy authority. The
    snapshot_hash matches the cfg:&lt;sha&gt; identifier the kernel
    propagates on policy decisions; clients pass it back on PUT for
    optimistic concurrency.

        Attributes:
            snapshot_version (Union[Unset, str]):
            snapshot_hash (Union[Unset, str]):
            updated_at (Union[Unset, datetime.datetime]):
            sections (Union[Unset, PolicyGlobalDocumentSections]): Keyed by section name — input_rules, output_rules,
                edge_action_rules, mcp_tool_rules, invariants.
    """

    snapshot_version: Union[Unset, str] = UNSET
    snapshot_hash: Union[Unset, str] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    sections: Union[Unset, "PolicyGlobalDocumentSections"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_global_document_sections import PolicyGlobalDocumentSections

        snapshot_version = self.snapshot_version

        snapshot_hash = self.snapshot_hash

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        sections: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.sections, Unset):
            sections = self.sections.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if snapshot_version is not UNSET:
            field_dict["snapshot_version"] = snapshot_version
        if snapshot_hash is not UNSET:
            field_dict["snapshot_hash"] = snapshot_hash
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if sections is not UNSET:
            field_dict["sections"] = sections

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_global_document_sections import PolicyGlobalDocumentSections

        d = src_dict.copy()
        snapshot_version = d.pop("snapshot_version", UNSET)

        snapshot_hash = d.pop("snapshot_hash", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        _sections = d.pop("sections", UNSET)
        sections: Union[Unset, PolicyGlobalDocumentSections]
        if isinstance(_sections, Unset):
            sections = UNSET
        else:
            sections = PolicyGlobalDocumentSections.from_dict(_sections)

        policy_global_document = cls(
            snapshot_version=snapshot_version,
            snapshot_hash=snapshot_hash,
            updated_at=updated_at,
            sections=sections,
        )

        policy_global_document.additional_properties = d
        return policy_global_document

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
