from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.eval_run_accepted_response_status import EvalRunAcceptedResponseStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
from uuid import UUID
import datetime


T = TypeVar("T", bound="EvalRunAcceptedResponse")


@_attrs_define
class EvalRunAcceptedResponse:
    """
    Attributes:
        run_id (UUID):
        status (EvalRunAcceptedResponseStatus):
        poll_url (Union[Unset, str]):
        dataset_id (Union[Unset, str]):
        dataset_name (Union[Unset, str]):
        dataset_version (Union[Unset, int]):
        tenant (Union[Unset, str]):
        started_at (Union[Unset, datetime.datetime]):
        completed_at (Union[Unset, datetime.datetime]):
        policy_snapshot (Union[Unset, str]):
        error (Union[Unset, str]):
    """

    run_id: UUID
    status: EvalRunAcceptedResponseStatus
    poll_url: Union[Unset, str] = UNSET
    dataset_id: Union[Unset, str] = UNSET
    dataset_name: Union[Unset, str] = UNSET
    dataset_version: Union[Unset, int] = UNSET
    tenant: Union[Unset, str] = UNSET
    started_at: Union[Unset, datetime.datetime] = UNSET
    completed_at: Union[Unset, datetime.datetime] = UNSET
    policy_snapshot: Union[Unset, str] = UNSET
    error: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        run_id = str(self.run_id)

        status = self.status.value

        poll_url = self.poll_url

        dataset_id = self.dataset_id

        dataset_name = self.dataset_name

        dataset_version = self.dataset_version

        tenant = self.tenant

        started_at: Union[Unset, str] = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        completed_at: Union[Unset, str] = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        policy_snapshot = self.policy_snapshot

        error = self.error

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "status": status,
            }
        )
        if poll_url is not UNSET:
            field_dict["poll_url"] = poll_url
        if dataset_id is not UNSET:
            field_dict["dataset_id"] = dataset_id
        if dataset_name is not UNSET:
            field_dict["dataset_name"] = dataset_name
        if dataset_version is not UNSET:
            field_dict["dataset_version"] = dataset_version
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if policy_snapshot is not UNSET:
            field_dict["policy_snapshot"] = policy_snapshot
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        run_id = UUID(d.pop("run_id"))

        status = EvalRunAcceptedResponseStatus(d.pop("status"))

        poll_url = d.pop("poll_url", UNSET)

        dataset_id = d.pop("dataset_id", UNSET)

        dataset_name = d.pop("dataset_name", UNSET)

        dataset_version = d.pop("dataset_version", UNSET)

        tenant = d.pop("tenant", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: Union[Unset, datetime.datetime]
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = isoparse(_started_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: Union[Unset, datetime.datetime]
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = isoparse(_completed_at)

        policy_snapshot = d.pop("policy_snapshot", UNSET)

        error = d.pop("error", UNSET)

        eval_run_accepted_response = cls(
            run_id=run_id,
            status=status,
            poll_url=poll_url,
            dataset_id=dataset_id,
            dataset_name=dataset_name,
            dataset_version=dataset_version,
            tenant=tenant,
            started_at=started_at,
            completed_at=completed_at,
            policy_snapshot=policy_snapshot,
            error=error,
        )

        eval_run_accepted_response.additional_properties = d
        return eval_run_accepted_response

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
