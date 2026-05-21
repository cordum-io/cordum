from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.copilot_session_job_metadata import CopilotSessionJobMetadata


T = TypeVar("T", bound="CopilotSessionJob")


@_attrs_define
class CopilotSessionJob:
    """
    Attributes:
        id (str):
        status (str):
        capabilities (List[str]):
        risk_tags (List[str]):
        metadata (CopilotSessionJobMetadata):
        type (Union[Unset, str]):
        topic (Union[Unset, str]):
        pool (Union[Unset, str]):
        created_at (Union[Unset, datetime.datetime]):
        updated_at (Union[Unset, datetime.datetime]):
    """

    id: str
    status: str
    capabilities: List[str]
    risk_tags: List[str]
    metadata: "CopilotSessionJobMetadata"
    type: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    pool: Union[Unset, str] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.copilot_session_job_metadata import CopilotSessionJobMetadata

        id = self.id

        status = self.status

        capabilities = self.capabilities

        risk_tags = self.risk_tags

        metadata = self.metadata.to_dict()

        type = self.type

        topic = self.topic

        pool = self.pool

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "status": status,
                "capabilities": capabilities,
                "riskTags": risk_tags,
                "metadata": metadata,
            }
        )
        if type is not UNSET:
            field_dict["type"] = type
        if topic is not UNSET:
            field_dict["topic"] = topic
        if pool is not UNSET:
            field_dict["pool"] = pool
        if created_at is not UNSET:
            field_dict["createdAt"] = created_at
        if updated_at is not UNSET:
            field_dict["updatedAt"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.copilot_session_job_metadata import CopilotSessionJobMetadata

        d = src_dict.copy()
        id = d.pop("id")

        status = d.pop("status")

        capabilities = cast(List[str], d.pop("capabilities"))

        risk_tags = cast(List[str], d.pop("riskTags"))

        metadata = CopilotSessionJobMetadata.from_dict(d.pop("metadata"))

        type = d.pop("type", UNSET)

        topic = d.pop("topic", UNSET)

        pool = d.pop("pool", UNSET)

        _created_at = d.pop("createdAt", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        _updated_at = d.pop("updatedAt", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        copilot_session_job = cls(
            id=id,
            status=status,
            capabilities=capabilities,
            risk_tags=risk_tags,
            metadata=metadata,
            type=type,
            topic=topic,
            pool=pool,
            created_at=created_at,
            updated_at=updated_at,
        )

        copilot_session_job.additional_properties = d
        return copilot_session_job

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
