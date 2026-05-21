from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.post_chat_request_role import PostChatRequestRole
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PostChatRequest")


@_attrs_define
class PostChatRequest:
    """
    Attributes:
        content (str):
        role (PostChatRequestRole):
        step_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
    """

    content: str
    role: PostChatRequestRole
    step_id: Union[Unset, str] = UNSET
    job_id: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        content = self.content

        role = self.role.value

        step_id = self.step_id

        job_id = self.job_id

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "content": content,
                "role": role,
            }
        )
        if step_id is not UNSET:
            field_dict["step_id"] = step_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        content = d.pop("content")

        role = PostChatRequestRole(d.pop("role"))

        step_id = d.pop("step_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        post_chat_request = cls(
            content=content,
            role=role,
            step_id=step_id,
            job_id=job_id,
        )

        post_chat_request.additional_properties = d
        return post_chat_request

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
