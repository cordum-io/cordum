from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, Union
from typing import Union


T = TypeVar("T", bound="EvalRunSummary")


@_attrs_define
class EvalRunSummary:
    """
    Attributes:
        total (int):
        passed (int):
        failed (int):
        regressions (int):
        errored (int):
        score_percent (Union[None, Unset, float]):
    """

    total: int
    passed: int
    failed: int
    regressions: int
    errored: int
    score_percent: Union[None, Unset, float] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        total = self.total

        passed = self.passed

        failed = self.failed

        regressions = self.regressions

        errored = self.errored

        score_percent: Union[None, Unset, float]
        if isinstance(self.score_percent, Unset):
            score_percent = UNSET
        else:
            score_percent = self.score_percent

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "total": total,
                "passed": passed,
                "failed": failed,
                "regressions": regressions,
                "errored": errored,
            }
        )
        if score_percent is not UNSET:
            field_dict["score_percent"] = score_percent

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        total = d.pop("total")

        passed = d.pop("passed")

        failed = d.pop("failed")

        regressions = d.pop("regressions")

        errored = d.pop("errored")

        def _parse_score_percent(data: object) -> Union[None, Unset, float]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, float], data)

        score_percent = _parse_score_percent(d.pop("score_percent", UNSET))

        eval_run_summary = cls(
            total=total,
            passed=passed,
            failed=failed,
            regressions=regressions,
            errored=errored,
            score_percent=score_percent,
        )

        eval_run_summary.additional_properties = d
        return eval_run_summary

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
