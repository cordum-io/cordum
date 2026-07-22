from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset







T = TypeVar("T", bound="ResolveMemoryResourceResponse200")


@_attrs_define
class ResolveMemoryResourceResponse200:
    """ 
        Attributes:
            media_type (str):
            size_bytes (int):
            base64 (str):
     """

    media_type: str
    size_bytes: int
    base64: str


    def to_dict(self) -> Dict[str, Any]:
        media_type = self.media_type

        size_bytes = self.size_bytes

        base64 = self.base64


        field_dict: Dict[str, Any] = {}
        field_dict.update({
            "media_type": media_type,
            "size_bytes": size_bytes,
            "base64": base64,
        })

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        media_type = d.pop("media_type")

        size_bytes = d.pop("size_bytes")

        base64 = d.pop("base64")

        resolve_memory_resource_response_200 = cls(
            media_type=media_type,
            size_bytes=size_bytes,
            base64=base64,
        )

        return resolve_memory_resource_response_200

