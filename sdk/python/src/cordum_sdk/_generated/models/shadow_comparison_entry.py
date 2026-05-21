from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.shadow_comparison_entry_diff import ShadowComparisonEntryDiff
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="ShadowComparisonEntry")


@_attrs_define
class ShadowComparisonEntry:
    """
    Attributes:
        ts_ms (Union[Unset, int]):
        job_id (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        shadow_bundle_id (Union[Unset, str]):
        active_verdict (Union[Unset, str]):
        shadow_verdict (Union[Unset, str]):
        diff (Union[Unset, ShadowComparisonEntryDiff]):
        active_rule_id (Union[Unset, str]):
        shadow_rule_id (Union[Unset, str]):
        latency_ms (Union[Unset, str]): Shadow-evaluation latency in ms, stored as a string in the audit event Extra
            map.
        seq (Union[Unset, int]):
    """

    ts_ms: Union[Unset, int] = UNSET
    job_id: Union[Unset, str] = UNSET
    agent_id: Union[Unset, str] = UNSET
    shadow_bundle_id: Union[Unset, str] = UNSET
    active_verdict: Union[Unset, str] = UNSET
    shadow_verdict: Union[Unset, str] = UNSET
    diff: Union[Unset, ShadowComparisonEntryDiff] = UNSET
    active_rule_id: Union[Unset, str] = UNSET
    shadow_rule_id: Union[Unset, str] = UNSET
    latency_ms: Union[Unset, str] = UNSET
    seq: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        ts_ms = self.ts_ms

        job_id = self.job_id

        agent_id = self.agent_id

        shadow_bundle_id = self.shadow_bundle_id

        active_verdict = self.active_verdict

        shadow_verdict = self.shadow_verdict

        diff: Union[Unset, str] = UNSET
        if not isinstance(self.diff, Unset):
            diff = self.diff.value

        active_rule_id = self.active_rule_id

        shadow_rule_id = self.shadow_rule_id

        latency_ms = self.latency_ms

        seq = self.seq

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if ts_ms is not UNSET:
            field_dict["ts_ms"] = ts_ms
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if shadow_bundle_id is not UNSET:
            field_dict["shadow_bundle_id"] = shadow_bundle_id
        if active_verdict is not UNSET:
            field_dict["active_verdict"] = active_verdict
        if shadow_verdict is not UNSET:
            field_dict["shadow_verdict"] = shadow_verdict
        if diff is not UNSET:
            field_dict["diff"] = diff
        if active_rule_id is not UNSET:
            field_dict["active_rule_id"] = active_rule_id
        if shadow_rule_id is not UNSET:
            field_dict["shadow_rule_id"] = shadow_rule_id
        if latency_ms is not UNSET:
            field_dict["latency_ms"] = latency_ms
        if seq is not UNSET:
            field_dict["seq"] = seq

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        ts_ms = d.pop("ts_ms", UNSET)

        job_id = d.pop("job_id", UNSET)

        agent_id = d.pop("agent_id", UNSET)

        shadow_bundle_id = d.pop("shadow_bundle_id", UNSET)

        active_verdict = d.pop("active_verdict", UNSET)

        shadow_verdict = d.pop("shadow_verdict", UNSET)

        _diff = d.pop("diff", UNSET)
        diff: Union[Unset, ShadowComparisonEntryDiff]
        if isinstance(_diff, Unset):
            diff = UNSET
        else:
            diff = ShadowComparisonEntryDiff(_diff)

        active_rule_id = d.pop("active_rule_id", UNSET)

        shadow_rule_id = d.pop("shadow_rule_id", UNSET)

        latency_ms = d.pop("latency_ms", UNSET)

        seq = d.pop("seq", UNSET)

        shadow_comparison_entry = cls(
            ts_ms=ts_ms,
            job_id=job_id,
            agent_id=agent_id,
            shadow_bundle_id=shadow_bundle_id,
            active_verdict=active_verdict,
            shadow_verdict=shadow_verdict,
            diff=diff,
            active_rule_id=active_rule_id,
            shadow_rule_id=shadow_rule_id,
            latency_ms=latency_ms,
            seq=seq,
        )

        shadow_comparison_entry.additional_properties = d
        return shadow_comparison_entry

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
