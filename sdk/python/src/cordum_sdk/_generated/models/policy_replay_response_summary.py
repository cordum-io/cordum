from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PolicyReplayResponseSummary")


@_attrs_define
class PolicyReplayResponseSummary:
    """
    Attributes:
        total_jobs (Union[Unset, int]):
        evaluated (Union[Unset, int]):
        escalated (Union[Unset, int]):
        relaxed (Union[Unset, int]):
        unchanged (Union[Unset, int]):
        errored (Union[Unset, int]):
    """

    total_jobs: Union[Unset, int] = UNSET
    evaluated: Union[Unset, int] = UNSET
    escalated: Union[Unset, int] = UNSET
    relaxed: Union[Unset, int] = UNSET
    unchanged: Union[Unset, int] = UNSET
    errored: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        total_jobs = self.total_jobs

        evaluated = self.evaluated

        escalated = self.escalated

        relaxed = self.relaxed

        unchanged = self.unchanged

        errored = self.errored

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if total_jobs is not UNSET:
            field_dict["total_jobs"] = total_jobs
        if evaluated is not UNSET:
            field_dict["evaluated"] = evaluated
        if escalated is not UNSET:
            field_dict["escalated"] = escalated
        if relaxed is not UNSET:
            field_dict["relaxed"] = relaxed
        if unchanged is not UNSET:
            field_dict["unchanged"] = unchanged
        if errored is not UNSET:
            field_dict["errored"] = errored

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        total_jobs = d.pop("total_jobs", UNSET)

        evaluated = d.pop("evaluated", UNSET)

        escalated = d.pop("escalated", UNSET)

        relaxed = d.pop("relaxed", UNSET)

        unchanged = d.pop("unchanged", UNSET)

        errored = d.pop("errored", UNSET)

        policy_replay_response_summary = cls(
            total_jobs=total_jobs,
            evaluated=evaluated,
            escalated=escalated,
            relaxed=relaxed,
            unchanged=unchanged,
            errored=errored,
        )

        policy_replay_response_summary.additional_properties = d
        return policy_replay_response_summary

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
