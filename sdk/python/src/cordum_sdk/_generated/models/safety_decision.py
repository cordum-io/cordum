from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.safety_decision_action import SafetyDecisionAction
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.safety_decision_constraints_type_0 import SafetyDecisionConstraintsType0


T = TypeVar("T", bound="SafetyDecision")


@_attrs_define
class SafetyDecision:
    """Result of safety kernel policy evaluation for a job

    Attributes:
        rule_id (Union[Unset, str]): ID of the matched policy rule
        action (Union[Unset, SafetyDecisionAction]): Safety decision outcome
        reason (Union[Unset, str]): Human-readable explanation for the decision
        constraints (Union['SafetyDecisionConstraintsType0', None, Unset]): Policy constraints applied if action is
            ALLOW_WITH_CONSTRAINTS
        evaluated_at (Union[Unset, datetime.datetime]): Timestamp of the policy evaluation
    """

    rule_id: Union[Unset, str] = UNSET
    action: Union[Unset, SafetyDecisionAction] = UNSET
    reason: Union[Unset, str] = UNSET
    constraints: Union["SafetyDecisionConstraintsType0", None, Unset] = UNSET
    evaluated_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.safety_decision_constraints_type_0 import SafetyDecisionConstraintsType0

        rule_id = self.rule_id

        action: Union[Unset, str] = UNSET
        if not isinstance(self.action, Unset):
            action = self.action.value

        reason = self.reason

        constraints: Union[Dict[str, Any], None, Unset]
        if isinstance(self.constraints, Unset):
            constraints = UNSET
        elif isinstance(self.constraints, SafetyDecisionConstraintsType0):
            constraints = self.constraints.to_dict()
        else:
            constraints = self.constraints

        evaluated_at: Union[Unset, str] = UNSET
        if not isinstance(self.evaluated_at, Unset):
            evaluated_at = self.evaluated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if action is not UNSET:
            field_dict["action"] = action
        if reason is not UNSET:
            field_dict["reason"] = reason
        if constraints is not UNSET:
            field_dict["constraints"] = constraints
        if evaluated_at is not UNSET:
            field_dict["evaluated_at"] = evaluated_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.safety_decision_constraints_type_0 import SafetyDecisionConstraintsType0

        d = src_dict.copy()
        rule_id = d.pop("rule_id", UNSET)

        _action = d.pop("action", UNSET)
        action: Union[Unset, SafetyDecisionAction]
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = SafetyDecisionAction(_action)

        reason = d.pop("reason", UNSET)

        def _parse_constraints(
            data: object,
        ) -> Union["SafetyDecisionConstraintsType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                constraints_type_0 = SafetyDecisionConstraintsType0.from_dict(data)

                return constraints_type_0
            except:  # noqa: E722
                pass
            return cast(Union["SafetyDecisionConstraintsType0", None, Unset], data)

        constraints = _parse_constraints(d.pop("constraints", UNSET))

        _evaluated_at = d.pop("evaluated_at", UNSET)
        evaluated_at: Union[Unset, datetime.datetime]
        if isinstance(_evaluated_at, Unset):
            evaluated_at = UNSET
        else:
            evaluated_at = isoparse(_evaluated_at)

        safety_decision = cls(
            rule_id=rule_id,
            action=action,
            reason=reason,
            constraints=constraints,
            evaluated_at=evaluated_at,
        )

        safety_decision.additional_properties = d
        return safety_decision

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
