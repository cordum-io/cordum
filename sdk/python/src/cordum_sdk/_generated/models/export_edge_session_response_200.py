from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.export_edge_session_response_200_redaction_level import (
    ExportEdgeSessionResponse200RedactionLevel,
)
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="ExportEdgeSessionResponse200")


@_attrs_define
class ExportEdgeSessionResponse200:
    """SessionExportBundle. See docs/edge-export.md for full schema
    including artifact_type catalog and missing_artifacts reasons.

        Attributes:
            manifest_version (str):  Example: edge.export.v1.
            generated_at (datetime.datetime):
            tenant_id (str):
            redaction_level (Union[Unset, ExportEdgeSessionResponse200RedactionLevel]):
    """

    manifest_version: str
    generated_at: datetime.datetime
    tenant_id: str
    redaction_level: Union[Unset, ExportEdgeSessionResponse200RedactionLevel] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        manifest_version = self.manifest_version

        generated_at = self.generated_at.isoformat()

        tenant_id = self.tenant_id

        redaction_level: Union[Unset, str] = UNSET
        if not isinstance(self.redaction_level, Unset):
            redaction_level = self.redaction_level.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "manifest_version": manifest_version,
                "generated_at": generated_at,
                "tenant_id": tenant_id,
            }
        )
        if redaction_level is not UNSET:
            field_dict["redaction_level"] = redaction_level

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        manifest_version = d.pop("manifest_version")

        generated_at = isoparse(d.pop("generated_at"))

        tenant_id = d.pop("tenant_id")

        _redaction_level = d.pop("redaction_level", UNSET)
        redaction_level: Union[Unset, ExportEdgeSessionResponse200RedactionLevel]
        if isinstance(_redaction_level, Unset):
            redaction_level = UNSET
        else:
            redaction_level = ExportEdgeSessionResponse200RedactionLevel(_redaction_level)

        export_edge_session_response_200 = cls(
            manifest_version=manifest_version,
            generated_at=generated_at,
            tenant_id=tenant_id,
            redaction_level=redaction_level,
        )

        export_edge_session_response_200.additional_properties = d
        return export_edge_session_response_200

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
