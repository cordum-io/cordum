from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from dateutil.parser import isoparse
from typing import cast
import datetime






T = TypeVar("T", bound="ResolveMemoryResourceBodyReference")


@_attrs_define
class ResolveMemoryResourceBodyReference:
    """ 
        Attributes:
            resolver_id (str):
            uri (str):
            sha256 (str):
            media_type (str):
            size_bytes (int):
            expires_at (datetime.datetime):
            purpose (str):
     """

    resolver_id: str
    uri: str
    sha256: str
    media_type: str
    size_bytes: int
    expires_at: datetime.datetime
    purpose: str


    def to_dict(self) -> Dict[str, Any]:
        resolver_id = self.resolver_id

        uri = self.uri

        sha256 = self.sha256

        media_type = self.media_type

        size_bytes = self.size_bytes

        expires_at = self.expires_at.isoformat()

        purpose = self.purpose


        field_dict: Dict[str, Any] = {}
        field_dict.update({
            "resolverId": resolver_id,
            "uri": uri,
            "sha256": sha256,
            "mediaType": media_type,
            "sizeBytes": size_bytes,
            "expiresAt": expires_at,
            "purpose": purpose,
        })

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        resolver_id = d.pop("resolverId")

        uri = d.pop("uri")

        sha256 = d.pop("sha256")

        media_type = d.pop("mediaType")

        size_bytes = d.pop("sizeBytes")

        expires_at = isoparse(d.pop("expiresAt"))




        purpose = d.pop("purpose")

        resolve_memory_resource_body_reference = cls(
            resolver_id=resolver_id,
            uri=uri,
            sha256=sha256,
            media_type=media_type,
            size_bytes=size_bytes,
            expires_at=expires_at,
            purpose=purpose,
        )

        return resolve_memory_resource_body_reference

