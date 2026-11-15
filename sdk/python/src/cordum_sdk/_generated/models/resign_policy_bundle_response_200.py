from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from dateutil.parser import isoparse
from typing import cast
import datetime


T = TypeVar("T", bound="ResignPolicyBundleResponse200")


@_attrs_define
class ResignPolicyBundleResponse200:
    """
    Attributes:
        id (str):
        key_id (str):
        hash_ (str):
        updated_at (datetime.datetime):
    """

    id: str
    key_id: str
    hash_: str
    updated_at: datetime.datetime
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        key_id = self.key_id

        hash_ = self.hash_

        updated_at = self.updated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "key_id": key_id,
                "hash": hash_,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id")

        key_id = d.pop("key_id")

        hash_ = d.pop("hash")

        updated_at = isoparse(d.pop("updated_at"))

        resign_policy_bundle_response_200 = cls(
            id=id,
            key_id=key_id,
            hash_=hash_,
            updated_at=updated_at,
        )

        resign_policy_bundle_response_200.additional_properties = d
        return resign_policy_bundle_response_200

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
