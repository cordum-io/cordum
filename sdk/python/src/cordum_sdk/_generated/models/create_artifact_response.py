from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="CreateArtifactResponse")


@_attrs_define
class CreateArtifactResponse:
    """
    Attributes:
        artifact_ptr (Union[Unset, str]):
        size_bytes (Union[Unset, int]):
    """

    artifact_ptr: Union[Unset, str] = UNSET
    size_bytes: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        artifact_ptr = self.artifact_ptr

        size_bytes = self.size_bytes

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if artifact_ptr is not UNSET:
            field_dict["artifact_ptr"] = artifact_ptr
        if size_bytes is not UNSET:
            field_dict["size_bytes"] = size_bytes

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        artifact_ptr = d.pop("artifact_ptr", UNSET)

        size_bytes = d.pop("size_bytes", UNSET)

        create_artifact_response = cls(
            artifact_ptr=artifact_ptr,
            size_bytes=size_bytes,
        )

        create_artifact_response.additional_properties = d
        return create_artifact_response

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
