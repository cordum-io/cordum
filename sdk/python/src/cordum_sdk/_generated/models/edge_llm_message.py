from typing import Any, Dict, Type, TypeVar


from attrs import define as _attrs_define


T = TypeVar("T", bound="EdgeLLMMessage")


@_attrs_define
class EdgeLLMMessage:
    """One role-tagged chat message; content is redacted before persistence.

    Attributes:
        role (str):
        content (str): Message text; bounded by the 1 MiB raw-envelope cap (MaxLLMRawEnvelopeBytes in
            core/edge/llmingest) and redacted by the gateway before persistence.
    """

    role: str
    content: str

    def to_dict(self) -> Dict[str, Any]:
        role = self.role

        content = self.content

        field_dict: Dict[str, Any] = {}
        field_dict.update(
            {
                "role": role,
                "content": content,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        role = d.pop("role")

        content = d.pop("content")

        edge_llm_message = cls(
            role=role,
            content=content,
        )

        return edge_llm_message
