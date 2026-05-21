from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.policy_check_response_decision import PolicyCheckResponseDecision
from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.policy_check_response_constraints_type_0 import (
        PolicyCheckResponseConstraintsType0,
    )
    from ..models.safety_decision import SafetyDecision


T = TypeVar("T", bound="PolicyCheckResponse")


@_attrs_define
class PolicyCheckResponse:
    """
    Attributes:
        decision (Union[Unset, PolicyCheckResponseDecision]):
        reason (Union[Unset, str]):
        rule_id (Union[None, Unset, str]):
        constraints (Union['PolicyCheckResponseConstraintsType0', None, Unset]):
        evaluations (Union[List['SafetyDecision'], None, Unset]):
    """

    decision: Union[Unset, PolicyCheckResponseDecision] = UNSET
    reason: Union[Unset, str] = UNSET
    rule_id: Union[None, Unset, str] = UNSET
    constraints: Union["PolicyCheckResponseConstraintsType0", None, Unset] = UNSET
    evaluations: Union[List["SafetyDecision"], None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_check_response_constraints_type_0 import (
            PolicyCheckResponseConstraintsType0,
        )
        from ..models.safety_decision import SafetyDecision

        decision: Union[Unset, str] = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.value

        reason = self.reason

        rule_id: Union[None, Unset, str]
        if isinstance(self.rule_id, Unset):
            rule_id = UNSET
        else:
            rule_id = self.rule_id

        constraints: Union[Dict[str, Any], None, Unset]
        if isinstance(self.constraints, Unset):
            constraints = UNSET
        elif isinstance(self.constraints, PolicyCheckResponseConstraintsType0):
            constraints = self.constraints.to_dict()
        else:
            constraints = self.constraints

        evaluations: Union[List[Dict[str, Any]], None, Unset]
        if isinstance(self.evaluations, Unset):
            evaluations = UNSET
        elif isinstance(self.evaluations, list):
            evaluations = []
            for evaluations_type_0_item_data in self.evaluations:
                evaluations_type_0_item = evaluations_type_0_item_data.to_dict()
                evaluations.append(evaluations_type_0_item)

        else:
            evaluations = self.evaluations

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if decision is not UNSET:
            field_dict["decision"] = decision
        if reason is not UNSET:
            field_dict["reason"] = reason
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if constraints is not UNSET:
            field_dict["constraints"] = constraints
        if evaluations is not UNSET:
            field_dict["evaluations"] = evaluations

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_check_response_constraints_type_0 import (
            PolicyCheckResponseConstraintsType0,
        )
        from ..models.safety_decision import SafetyDecision

        d = src_dict.copy()
        _decision = d.pop("decision", UNSET)
        decision: Union[Unset, PolicyCheckResponseDecision]
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = PolicyCheckResponseDecision(_decision)

        reason = d.pop("reason", UNSET)

        def _parse_rule_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        rule_id = _parse_rule_id(d.pop("rule_id", UNSET))

        def _parse_constraints(
            data: object,
        ) -> Union["PolicyCheckResponseConstraintsType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                constraints_type_0 = PolicyCheckResponseConstraintsType0.from_dict(data)

                return constraints_type_0
            except:  # noqa: E722
                pass
            return cast(Union["PolicyCheckResponseConstraintsType0", None, Unset], data)

        constraints = _parse_constraints(d.pop("constraints", UNSET))

        def _parse_evaluations(data: object) -> Union[List["SafetyDecision"], None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                evaluations_type_0 = []
                _evaluations_type_0 = data
                for evaluations_type_0_item_data in _evaluations_type_0:
                    evaluations_type_0_item = SafetyDecision.from_dict(evaluations_type_0_item_data)

                    evaluations_type_0.append(evaluations_type_0_item)

                return evaluations_type_0
            except:  # noqa: E722
                pass
            return cast(Union[List["SafetyDecision"], None, Unset], data)

        evaluations = _parse_evaluations(d.pop("evaluations", UNSET))

        policy_check_response = cls(
            decision=decision,
            reason=reason,
            rule_id=rule_id,
            constraints=constraints,
            evaluations=evaluations,
        )

        policy_check_response.additional_properties = d
        return policy_check_response

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
