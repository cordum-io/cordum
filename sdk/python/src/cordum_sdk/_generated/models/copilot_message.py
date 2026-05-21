from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.copilot_message_role import CopilotMessageRole
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="CopilotMessage")


@_attrs_define
class CopilotMessage:
    """
    Attributes:
        id (str):
        role (CopilotMessageRole):
        content (str):
        timestamp (datetime.datetime):
        job_ids (Union[Unset, List[str]]):
    """

    id: str
    role: CopilotMessageRole
    content: str
    timestamp: datetime.datetime
    job_ids: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        role = self.role.value

        content = self.content

        timestamp = self.timestamp.isoformat()

        job_ids: Union[Unset, List[str]] = UNSET
        if not isinstance(self.job_ids, Unset):
            job_ids = self.job_ids

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "role": role,
                "content": content,
                "timestamp": timestamp,
            }
        )
        if job_ids is not UNSET:
            field_dict["jobIds"] = job_ids

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id")

        role = CopilotMessageRole(d.pop("role"))

        content = d.pop("content")

        timestamp = isoparse(d.pop("timestamp"))

        job_ids = cast(List[str], d.pop("jobIds", UNSET))

        copilot_message = cls(
            id=id,
            role=role,
            content=content,
            timestamp=timestamp,
            job_ids=job_ids,
        )

        copilot_message.additional_properties = d
        return copilot_message

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
