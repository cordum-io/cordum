from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.chat_message_role import ChatMessageRole
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Union
import datetime


T = TypeVar("T", bound="ChatMessage")


@_attrs_define
class ChatMessage:
    """
    Attributes:
        id (Union[Unset, str]):
        run_id (Union[Unset, str]):
        content (Union[Unset, str]):
        role (Union[Unset, ChatMessageRole]):
        step_id (Union[None, Unset, str]):
        job_id (Union[None, Unset, str]):
        created_at (Union[Unset, datetime.datetime]):
    """

    id: Union[Unset, str] = UNSET
    run_id: Union[Unset, str] = UNSET
    content: Union[Unset, str] = UNSET
    role: Union[Unset, ChatMessageRole] = UNSET
    step_id: Union[None, Unset, str] = UNSET
    job_id: Union[None, Unset, str] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        run_id = self.run_id

        content = self.content

        role: Union[Unset, str] = UNSET
        if not isinstance(self.role, Unset):
            role = self.role.value

        step_id: Union[None, Unset, str]
        if isinstance(self.step_id, Unset):
            step_id = UNSET
        else:
            step_id = self.step_id

        job_id: Union[None, Unset, str]
        if isinstance(self.job_id, Unset):
            job_id = UNSET
        else:
            job_id = self.job_id

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if content is not UNSET:
            field_dict["content"] = content
        if role is not UNSET:
            field_dict["role"] = role
        if step_id is not UNSET:
            field_dict["step_id"] = step_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if created_at is not UNSET:
            field_dict["created_at"] = created_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        run_id = d.pop("run_id", UNSET)

        content = d.pop("content", UNSET)

        _role = d.pop("role", UNSET)
        role: Union[Unset, ChatMessageRole]
        if isinstance(_role, Unset):
            role = UNSET
        else:
            role = ChatMessageRole(_role)

        def _parse_step_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        step_id = _parse_step_id(d.pop("step_id", UNSET))

        def _parse_job_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        job_id = _parse_job_id(d.pop("job_id", UNSET))

        _created_at = d.pop("created_at", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        chat_message = cls(
            id=id,
            run_id=run_id,
            content=content,
            role=role,
            step_id=step_id,
            job_id=job_id,
            created_at=created_at,
        )

        chat_message.additional_properties = d
        return chat_message

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
