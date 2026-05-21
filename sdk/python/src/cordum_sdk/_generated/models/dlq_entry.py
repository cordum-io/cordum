from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="DLQEntry")


@_attrs_define
class DLQEntry:
    """
    Attributes:
        job_id (Union[Unset, str]):
        topic (Union[Unset, str]):
        tenant (Union[Unset, str]):
        error (Union[Unset, str]):
        failed_at (Union[Unset, datetime.datetime]):
        retry_count (Union[Unset, int]):
        original_state (Union[Unset, str]):
    """

    job_id: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    tenant: Union[Unset, str] = UNSET
    error: Union[Unset, str] = UNSET
    failed_at: Union[Unset, datetime.datetime] = UNSET
    retry_count: Union[Unset, int] = UNSET
    original_state: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        job_id = self.job_id

        topic = self.topic

        tenant = self.tenant

        error = self.error

        failed_at: Union[Unset, str] = UNSET
        if not isinstance(self.failed_at, Unset):
            failed_at = self.failed_at.isoformat()

        retry_count = self.retry_count

        original_state = self.original_state

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if topic is not UNSET:
            field_dict["topic"] = topic
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if error is not UNSET:
            field_dict["error"] = error
        if failed_at is not UNSET:
            field_dict["failed_at"] = failed_at
        if retry_count is not UNSET:
            field_dict["retry_count"] = retry_count
        if original_state is not UNSET:
            field_dict["original_state"] = original_state

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        job_id = d.pop("job_id", UNSET)

        topic = d.pop("topic", UNSET)

        tenant = d.pop("tenant", UNSET)

        error = d.pop("error", UNSET)

        _failed_at = d.pop("failed_at", UNSET)
        failed_at: Union[Unset, datetime.datetime]
        if isinstance(_failed_at, Unset):
            failed_at = UNSET
        else:
            failed_at = isoparse(_failed_at)

        retry_count = d.pop("retry_count", UNSET)

        original_state = d.pop("original_state", UNSET)

        dlq_entry = cls(
            job_id=job_id,
            topic=topic,
            tenant=tenant,
            error=error,
            failed_at=failed_at,
            retry_count=retry_count,
            original_state=original_state,
        )

        dlq_entry.additional_properties = d
        return dlq_entry

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
