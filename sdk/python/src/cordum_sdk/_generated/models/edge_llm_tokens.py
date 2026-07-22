from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union






T = TypeVar("T", bound="EdgeLLMTokens")


@_attrs_define
class EdgeLLMTokens:
    """ Optional token accounting for usage/cost evidence.

        Attributes:
            input_tokens (Union[Unset, int]):
            output_tokens (Union[Unset, int]):
            total_tokens (Union[Unset, int]):
     """

    input_tokens: Union[Unset, int] = UNSET
    output_tokens: Union[Unset, int] = UNSET
    total_tokens: Union[Unset, int] = UNSET


    def to_dict(self) -> Dict[str, Any]:
        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        total_tokens = self.total_tokens


        field_dict: Dict[str, Any] = {}
        field_dict.update({
        })
        if input_tokens is not UNSET:
            field_dict["input_tokens"] = input_tokens
        if output_tokens is not UNSET:
            field_dict["output_tokens"] = output_tokens
        if total_tokens is not UNSET:
            field_dict["total_tokens"] = total_tokens

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        input_tokens = d.pop("input_tokens", UNSET)

        output_tokens = d.pop("output_tokens", UNSET)

        total_tokens = d.pop("total_tokens", UNSET)

        edge_llm_tokens = cls(
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            total_tokens=total_tokens,
        )

        return edge_llm_tokens

