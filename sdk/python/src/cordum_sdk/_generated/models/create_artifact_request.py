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
    from ..models.create_artifact_request_labels import CreateArtifactRequestLabels


T = TypeVar("T", bound="CreateArtifactRequest")


@_attrs_define
class CreateArtifactRequest:
    """
    Attributes:
        content_base64 (str):
        content_type (str):
        retention (Union[Unset, str]): Retention policy (e.g., "30d", "permanent")
        labels (Union[Unset, CreateArtifactRequestLabels]):
    """

    content_base64: str
    content_type: str
    retention: Union[Unset, str] = UNSET
    labels: Union[Unset, "CreateArtifactRequestLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.create_artifact_request_labels import CreateArtifactRequestLabels

        content_base64 = self.content_base64

        content_type = self.content_type

        retention = self.retention

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "content_base64": content_base64,
                "content_type": content_type,
            }
        )
        if retention is not UNSET:
            field_dict["retention"] = retention
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.create_artifact_request_labels import CreateArtifactRequestLabels

        d = src_dict.copy()
        content_base64 = d.pop("content_base64")

        content_type = d.pop("content_type")

        retention = d.pop("retention", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, CreateArtifactRequestLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = CreateArtifactRequestLabels.from_dict(_labels)

        create_artifact_request = cls(
            content_base64=content_base64,
            content_type=content_type,
            retention=retention,
            labels=labels,
        )

        create_artifact_request.additional_properties = d
        return create_artifact_request

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
