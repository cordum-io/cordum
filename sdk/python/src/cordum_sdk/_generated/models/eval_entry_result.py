from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.eval_entry_result_drift_direction import EvalEntryResultDriftDirection
from ..models.eval_entry_result_status import EvalEntryResultStatus
from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.eval_entry_result_input import EvalEntryResultInput


T = TypeVar("T", bound="EvalEntryResult")


@_attrs_define
class EvalEntryResult:
    """
    Attributes:
        entry_id (str):
        status (EvalEntryResultStatus): regression = expected was deny/require_approval/throttle/allow_with_constraints
            AND actual is allow
        drift_direction (EvalEntryResultDriftDirection):
        input_ (Union[Unset, EvalEntryResultInput]):
        expected_decision (Union[Unset, str]):
        actual_decision (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        reason (Union[Unset, str]):
        error (Union[Unset, str]):
    """

    entry_id: str
    status: EvalEntryResultStatus
    drift_direction: EvalEntryResultDriftDirection
    input_: Union[Unset, "EvalEntryResultInput"] = UNSET
    expected_decision: Union[Unset, str] = UNSET
    actual_decision: Union[Unset, str] = UNSET
    rule_id: Union[Unset, str] = UNSET
    reason: Union[Unset, str] = UNSET
    error: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.eval_entry_result_input import EvalEntryResultInput

        entry_id = self.entry_id

        status = self.status.value

        drift_direction = self.drift_direction.value

        input_: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.input_, Unset):
            input_ = self.input_.to_dict()

        expected_decision = self.expected_decision

        actual_decision = self.actual_decision

        rule_id = self.rule_id

        reason = self.reason

        error = self.error

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "entry_id": entry_id,
                "status": status,
                "drift_direction": drift_direction,
            }
        )
        if input_ is not UNSET:
            field_dict["input"] = input_
        if expected_decision is not UNSET:
            field_dict["expected_decision"] = expected_decision
        if actual_decision is not UNSET:
            field_dict["actual_decision"] = actual_decision
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.eval_entry_result_input import EvalEntryResultInput

        d = src_dict.copy()
        entry_id = d.pop("entry_id")

        status = EvalEntryResultStatus(d.pop("status"))

        drift_direction = EvalEntryResultDriftDirection(d.pop("drift_direction"))

        _input_ = d.pop("input", UNSET)
        input_: Union[Unset, EvalEntryResultInput]
        if isinstance(_input_, Unset):
            input_ = UNSET
        else:
            input_ = EvalEntryResultInput.from_dict(_input_)

        expected_decision = d.pop("expected_decision", UNSET)

        actual_decision = d.pop("actual_decision", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        reason = d.pop("reason", UNSET)

        error = d.pop("error", UNSET)

        eval_entry_result = cls(
            entry_id=entry_id,
            status=status,
            drift_direction=drift_direction,
            input_=input_,
            expected_decision=expected_decision,
            actual_decision=actual_decision,
            rule_id=rule_id,
            reason=reason,
            error=error,
        )

        eval_entry_result.additional_properties = d
        return eval_entry_result

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
