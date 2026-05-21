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
    from ..models.artifact_detail_metadata_labels import ArtifactDetailMetadataLabels


T = TypeVar("T", bound="ArtifactDetailMetadata")


@_attrs_define
class ArtifactDetailMetadata:
    """
    Attributes:
        content_type (Union[Unset, str]):
        size_bytes (Union[Unset, int]):
        created_at (Union[Unset, datetime.datetime]):
        retention (Union[Unset, str]):
        labels (Union[Unset, ArtifactDetailMetadataLabels]):
    """

    content_type: Union[Unset, str] = UNSET
    size_bytes: Union[Unset, int] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    retention: Union[Unset, str] = UNSET
    labels: Union[Unset, "ArtifactDetailMetadataLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.artifact_detail_metadata_labels import ArtifactDetailMetadataLabels

        content_type = self.content_type

        size_bytes = self.size_bytes

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        retention = self.retention

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if content_type is not UNSET:
            field_dict["content_type"] = content_type
        if size_bytes is not UNSET:
            field_dict["size_bytes"] = size_bytes
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if retention is not UNSET:
            field_dict["retention"] = retention
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.artifact_detail_metadata_labels import ArtifactDetailMetadataLabels

        d = src_dict.copy()
        content_type = d.pop("content_type", UNSET)

        size_bytes = d.pop("size_bytes", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        retention = d.pop("retention", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, ArtifactDetailMetadataLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = ArtifactDetailMetadataLabels.from_dict(_labels)

        artifact_detail_metadata = cls(
            content_type=content_type,
            size_bytes=size_bytes,
            created_at=created_at,
            retention=retention,
            labels=labels,
        )

        artifact_detail_metadata.additional_properties = d
        return artifact_detail_metadata

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
