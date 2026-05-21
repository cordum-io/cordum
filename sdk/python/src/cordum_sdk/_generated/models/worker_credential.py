from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="WorkerCredential")


@_attrs_define
class WorkerCredential:
    """
    Attributes:
        worker_id (str):
        created_by (str):
        created_at (datetime.datetime):
        allowed_pools (Union[Unset, List[str]]):
        allowed_topics (Union[Unset, List[str]]):
        pack_id (Union[Unset, str]):
        revoked_at (Union[Unset, datetime.datetime]):
    """

    worker_id: str
    created_by: str
    created_at: datetime.datetime
    allowed_pools: Union[Unset, List[str]] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    pack_id: Union[Unset, str] = UNSET
    revoked_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        worker_id = self.worker_id

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        allowed_pools: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_pools, Unset):
            allowed_pools = self.allowed_pools

        allowed_topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_topics, Unset):
            allowed_topics = self.allowed_topics

        pack_id = self.pack_id

        revoked_at: Union[Unset, str] = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "worker_id": worker_id,
                "created_by": created_by,
                "created_at": created_at,
            }
        )
        if allowed_pools is not UNSET:
            field_dict["allowed_pools"] = allowed_pools
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if pack_id is not UNSET:
            field_dict["pack_id"] = pack_id
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        worker_id = d.pop("worker_id")

        created_by = d.pop("created_by")

        created_at = isoparse(d.pop("created_at"))

        allowed_pools = cast(List[str], d.pop("allowed_pools", UNSET))

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))

        pack_id = d.pop("pack_id", UNSET)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: Union[Unset, datetime.datetime]
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = isoparse(_revoked_at)

        worker_credential = cls(
            worker_id=worker_id,
            created_by=created_by,
            created_at=created_at,
            allowed_pools=allowed_pools,
            allowed_topics=allowed_topics,
            pack_id=pack_id,
            revoked_at=revoked_at,
        )

        worker_credential.additional_properties = d
        return worker_credential

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
