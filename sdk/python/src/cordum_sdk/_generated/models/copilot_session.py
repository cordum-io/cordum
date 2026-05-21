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
    from ..models.copilot_message import CopilotMessage
    from ..models.copilot_session_metadata import CopilotSessionMetadata


T = TypeVar("T", bound="CopilotSession")


@_attrs_define
class CopilotSession:
    """
    Attributes:
        id (str):
        user_id (str):
        created_at (datetime.datetime):
        updated_at (datetime.datetime):
        messages (List['CopilotMessage']):
        title (Union[Unset, str]):
        metadata (Union[Unset, CopilotSessionMetadata]):
    """

    id: str
    user_id: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    messages: List["CopilotMessage"]
    title: Union[Unset, str] = UNSET
    metadata: Union[Unset, "CopilotSessionMetadata"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.copilot_message import CopilotMessage
        from ..models.copilot_session_metadata import CopilotSessionMetadata

        id = self.id

        user_id = self.user_id

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        title = self.title

        metadata: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "userId": user_id,
                "createdAt": created_at,
                "updatedAt": updated_at,
                "messages": messages,
            }
        )
        if title is not UNSET:
            field_dict["title"] = title
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.copilot_message import CopilotMessage
        from ..models.copilot_session_metadata import CopilotSessionMetadata

        d = src_dict.copy()
        id = d.pop("id")

        user_id = d.pop("userId")

        created_at = isoparse(d.pop("createdAt"))

        updated_at = isoparse(d.pop("updatedAt"))

        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = CopilotMessage.from_dict(messages_item_data)

            messages.append(messages_item)

        title = d.pop("title", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, CopilotSessionMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = CopilotSessionMetadata.from_dict(_metadata)

        copilot_session = cls(
            id=id,
            user_id=user_id,
            created_at=created_at,
            updated_at=updated_at,
            messages=messages,
            title=title,
            metadata=metadata,
        )

        copilot_session.additional_properties = d
        return copilot_session

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
