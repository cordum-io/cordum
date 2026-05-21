from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="GetApprovalContextResponse200BlastRadius")


@_attrs_define
class GetApprovalContextResponse200BlastRadius:
    """
    Attributes:
        systems (Union[Unset, List[str]]):
        namespaces (Union[Unset, List[str]]):
        resources (Union[Unset, List[str]]):
        scope_description (Union[Unset, str]):
    """

    systems: Union[Unset, List[str]] = UNSET
    namespaces: Union[Unset, List[str]] = UNSET
    resources: Union[Unset, List[str]] = UNSET
    scope_description: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        systems: Union[Unset, List[str]] = UNSET
        if not isinstance(self.systems, Unset):
            systems = self.systems

        namespaces: Union[Unset, List[str]] = UNSET
        if not isinstance(self.namespaces, Unset):
            namespaces = self.namespaces

        resources: Union[Unset, List[str]] = UNSET
        if not isinstance(self.resources, Unset):
            resources = self.resources

        scope_description = self.scope_description

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if systems is not UNSET:
            field_dict["systems"] = systems
        if namespaces is not UNSET:
            field_dict["namespaces"] = namespaces
        if resources is not UNSET:
            field_dict["resources"] = resources
        if scope_description is not UNSET:
            field_dict["scope_description"] = scope_description

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        systems = cast(List[str], d.pop("systems", UNSET))

        namespaces = cast(List[str], d.pop("namespaces", UNSET))

        resources = cast(List[str], d.pop("resources", UNSET))

        scope_description = d.pop("scope_description", UNSET)

        get_approval_context_response_200_blast_radius = cls(
            systems=systems,
            namespaces=namespaces,
            resources=resources,
            scope_description=scope_description,
        )

        get_approval_context_response_200_blast_radius.additional_properties = d
        return get_approval_context_response_200_blast_radius

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
