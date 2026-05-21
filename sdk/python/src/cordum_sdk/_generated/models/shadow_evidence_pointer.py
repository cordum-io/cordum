from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.shadow_evidence_pointer_redaction_level import ShadowEvidencePointerRedactionLevel
from ..models.shadow_evidence_pointer_retention_class import ShadowEvidencePointerRetentionClass
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="ShadowEvidencePointer")


@_attrs_define
class ShadowEvidencePointer:
    """Reference to redacted evidence stored outside the finding record. Distinct from `ArtifactPointer` because shadow
    findings have no session/execution context.

        Attributes:
            tenant_id (str): MUST match the parent finding's tenant_id; cross-tenant pointers are rejected.
            uri (str):
            sha256 (str):
            retention_class (ShadowEvidencePointerRetentionClass):
            redaction_level (ShadowEvidencePointerRedactionLevel):
            created_at (datetime.datetime):
            size_bytes (Union[Unset, int]):
    """

    tenant_id: str
    uri: str
    sha256: str
    retention_class: ShadowEvidencePointerRetentionClass
    redaction_level: ShadowEvidencePointerRedactionLevel
    created_at: datetime.datetime
    size_bytes: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        tenant_id = self.tenant_id

        uri = self.uri

        sha256 = self.sha256

        retention_class = self.retention_class.value

        redaction_level = self.redaction_level.value

        created_at = self.created_at.isoformat()

        size_bytes = self.size_bytes

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "uri": uri,
                "sha256": sha256,
                "retention_class": retention_class,
                "redaction_level": redaction_level,
                "created_at": created_at,
            }
        )
        if size_bytes is not UNSET:
            field_dict["size_bytes"] = size_bytes

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        tenant_id = d.pop("tenant_id")

        uri = d.pop("uri")

        sha256 = d.pop("sha256")

        retention_class = ShadowEvidencePointerRetentionClass(d.pop("retention_class"))

        redaction_level = ShadowEvidencePointerRedactionLevel(d.pop("redaction_level"))

        created_at = isoparse(d.pop("created_at"))

        size_bytes = d.pop("size_bytes", UNSET)

        shadow_evidence_pointer = cls(
            tenant_id=tenant_id,
            uri=uri,
            sha256=sha256,
            retention_class=retention_class,
            redaction_level=redaction_level,
            created_at=created_at,
            size_bytes=size_bytes,
        )

        shadow_evidence_pointer.additional_properties = d
        return shadow_evidence_pointer

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
