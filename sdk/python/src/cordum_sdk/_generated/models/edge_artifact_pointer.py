from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_artifact_pointer_artifact_type import EdgeArtifactPointerArtifactType
from ..models.edge_artifact_pointer_redaction_level import EdgeArtifactPointerRedactionLevel
from ..models.edge_artifact_pointer_retention_class import EdgeArtifactPointerRetentionClass
from dateutil.parser import isoparse
from typing import cast
import datetime


T = TypeVar("T", bound="EdgeArtifactPointer")


@_attrs_define
class EdgeArtifactPointer:
    """Pointer to redacted evidence stored outside the event row. Its tenant/session/execution/event IDs must match the
    enclosing event, and the URI must be an internal artifact reference rather than a signed URL or token-bearing
    external URI.

        Attributes:
            artifact_type (EdgeArtifactPointerArtifactType):
            session_id (str):
            execution_id (str):
            event_id (str):
            tenant_id (str):
            retention_class (EdgeArtifactPointerRetentionClass):
            redaction_level (EdgeArtifactPointerRedactionLevel):
            sha256 (str):
            uri (str): Internal artifact URI such as `artifact://...` or `edge-artifact://...`; signed URLs, query-string
                tokens, userinfo, fragments, and external HTTP(S) storage URLs are rejected before persistence.
            created_at (datetime.datetime):
    """

    artifact_type: EdgeArtifactPointerArtifactType
    session_id: str
    execution_id: str
    event_id: str
    tenant_id: str
    retention_class: EdgeArtifactPointerRetentionClass
    redaction_level: EdgeArtifactPointerRedactionLevel
    sha256: str
    uri: str
    created_at: datetime.datetime
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        artifact_type = self.artifact_type.value

        session_id = self.session_id

        execution_id = self.execution_id

        event_id = self.event_id

        tenant_id = self.tenant_id

        retention_class = self.retention_class.value

        redaction_level = self.redaction_level.value

        sha256 = self.sha256

        uri = self.uri

        created_at = self.created_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "artifact_type": artifact_type,
                "session_id": session_id,
                "execution_id": execution_id,
                "event_id": event_id,
                "tenant_id": tenant_id,
                "retention_class": retention_class,
                "redaction_level": redaction_level,
                "sha256": sha256,
                "uri": uri,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        artifact_type = EdgeArtifactPointerArtifactType(d.pop("artifact_type"))

        session_id = d.pop("session_id")

        execution_id = d.pop("execution_id")

        event_id = d.pop("event_id")

        tenant_id = d.pop("tenant_id")

        retention_class = EdgeArtifactPointerRetentionClass(d.pop("retention_class"))

        redaction_level = EdgeArtifactPointerRedactionLevel(d.pop("redaction_level"))

        sha256 = d.pop("sha256")

        uri = d.pop("uri")

        created_at = isoparse(d.pop("created_at"))

        edge_artifact_pointer = cls(
            artifact_type=artifact_type,
            session_id=session_id,
            execution_id=execution_id,
            event_id=event_id,
            tenant_id=tenant_id,
            retention_class=retention_class,
            redaction_level=redaction_level,
            sha256=sha256,
            uri=uri,
            created_at=created_at,
        )

        edge_artifact_pointer.additional_properties = d
        return edge_artifact_pointer

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
