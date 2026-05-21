from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.update_policy_global_request_sections import UpdatePolicyGlobalRequestSections


T = TypeVar("T", bound="UpdatePolicyGlobalRequest")


@_attrs_define
class UpdatePolicyGlobalRequest:
    """
    Attributes:
        sections (UpdatePolicyGlobalRequestSections): Keyed by section name. Only listed sections are written; absent
            sections remain unchanged.
        snapshot_version (Union[Unset, str]): Optimistic concurrency token. When non-empty and != current snapshot,
            request is rejected with 409.
        author (Union[Unset, str]):
        message (Union[Unset, str]):
    """

    sections: "UpdatePolicyGlobalRequestSections"
    snapshot_version: Union[Unset, str] = UNSET
    author: Union[Unset, str] = UNSET
    message: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.update_policy_global_request_sections import UpdatePolicyGlobalRequestSections

        sections = self.sections.to_dict()

        snapshot_version = self.snapshot_version

        author = self.author

        message = self.message

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sections": sections,
            }
        )
        if snapshot_version is not UNSET:
            field_dict["snapshot_version"] = snapshot_version
        if author is not UNSET:
            field_dict["author"] = author
        if message is not UNSET:
            field_dict["message"] = message

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.update_policy_global_request_sections import UpdatePolicyGlobalRequestSections

        d = src_dict.copy()
        sections = UpdatePolicyGlobalRequestSections.from_dict(d.pop("sections"))

        snapshot_version = d.pop("snapshot_version", UNSET)

        author = d.pop("author", UNSET)

        message = d.pop("message", UNSET)

        update_policy_global_request = cls(
            sections=sections,
            snapshot_version=snapshot_version,
            author=author,
            message=message,
        )

        update_policy_global_request.additional_properties = d
        return update_policy_global_request

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
