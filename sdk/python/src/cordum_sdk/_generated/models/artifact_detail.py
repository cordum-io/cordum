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
    from ..models.artifact_detail_metadata import ArtifactDetailMetadata


T = TypeVar("T", bound="ArtifactDetail")


@_attrs_define
class ArtifactDetail:
    """
    Attributes:
        artifact_ptr (Union[Unset, str]):
        content_base64 (Union[Unset, str]):
        metadata (Union[Unset, ArtifactDetailMetadata]):
    """

    artifact_ptr: Union[Unset, str] = UNSET
    content_base64: Union[Unset, str] = UNSET
    metadata: Union[Unset, "ArtifactDetailMetadata"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.artifact_detail_metadata import ArtifactDetailMetadata

        artifact_ptr = self.artifact_ptr

        content_base64 = self.content_base64

        metadata: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if artifact_ptr is not UNSET:
            field_dict["artifact_ptr"] = artifact_ptr
        if content_base64 is not UNSET:
            field_dict["content_base64"] = content_base64
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.artifact_detail_metadata import ArtifactDetailMetadata

        d = src_dict.copy()
        artifact_ptr = d.pop("artifact_ptr", UNSET)

        content_base64 = d.pop("content_base64", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, ArtifactDetailMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ArtifactDetailMetadata.from_dict(_metadata)

        artifact_detail = cls(
            artifact_ptr=artifact_ptr,
            content_base64=content_base64,
            metadata=metadata,
        )

        artifact_detail.additional_properties = d
        return artifact_detail

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
