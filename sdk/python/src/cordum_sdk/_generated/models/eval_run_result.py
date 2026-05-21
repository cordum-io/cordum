from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from uuid import UUID
import datetime

if TYPE_CHECKING:
    from ..models.eval_run_summary import EvalRunSummary
    from ..models.eval_entry_result import EvalEntryResult


T = TypeVar("T", bound="EvalRunResult")


@_attrs_define
class EvalRunResult:
    """
    Attributes:
        run_id (UUID):
        dataset_id (str):
        dataset_name (str):
        dataset_version (int):
        tenant (str):
        policy_snapshot (str):
        started_at (datetime.datetime):
        completed_at (datetime.datetime):
        summary (EvalRunSummary):
        entries (List['EvalEntryResult']):
    """

    run_id: UUID
    dataset_id: str
    dataset_name: str
    dataset_version: int
    tenant: str
    policy_snapshot: str
    started_at: datetime.datetime
    completed_at: datetime.datetime
    summary: "EvalRunSummary"
    entries: List["EvalEntryResult"]
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.eval_run_summary import EvalRunSummary
        from ..models.eval_entry_result import EvalEntryResult

        run_id = str(self.run_id)

        dataset_id = self.dataset_id

        dataset_name = self.dataset_name

        dataset_version = self.dataset_version

        tenant = self.tenant

        policy_snapshot = self.policy_snapshot

        started_at = self.started_at.isoformat()

        completed_at = self.completed_at.isoformat()

        summary = self.summary.to_dict()

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "dataset_id": dataset_id,
                "dataset_name": dataset_name,
                "dataset_version": dataset_version,
                "tenant": tenant,
                "policy_snapshot": policy_snapshot,
                "started_at": started_at,
                "completed_at": completed_at,
                "summary": summary,
                "entries": entries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.eval_run_summary import EvalRunSummary
        from ..models.eval_entry_result import EvalEntryResult

        d = src_dict.copy()
        run_id = UUID(d.pop("run_id"))

        dataset_id = d.pop("dataset_id")

        dataset_name = d.pop("dataset_name")

        dataset_version = d.pop("dataset_version")

        tenant = d.pop("tenant")

        policy_snapshot = d.pop("policy_snapshot")

        started_at = isoparse(d.pop("started_at"))

        completed_at = isoparse(d.pop("completed_at"))

        summary = EvalRunSummary.from_dict(d.pop("summary"))

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = EvalEntryResult.from_dict(entries_item_data)

            entries.append(entries_item)

        eval_run_result = cls(
            run_id=run_id,
            dataset_id=dataset_id,
            dataset_name=dataset_name,
            dataset_version=dataset_version,
            tenant=tenant,
            policy_snapshot=policy_snapshot,
            started_at=started_at,
            completed_at=completed_at,
            summary=summary,
            entries=entries,
        )

        eval_run_result.additional_properties = d
        return eval_run_result

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
