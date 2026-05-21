from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_evaluate_response_decision import EdgeEvaluateResponseDecision
from ..models.edge_evaluate_response_error_code import EdgeEvaluateResponseErrorCode
from ..models.edge_evaluate_response_permission_decision import (
    EdgeEvaluateResponsePermissionDecision,
)
from ..models.edge_evaluate_response_wait_strategy import EdgeEvaluateResponseWaitStrategy
from ..types import UNSET, Unset
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.edge_evaluate_response_constraints_type_0 import (
        EdgeEvaluateResponseConstraintsType0,
    )
    from ..models.edge_evaluate_response_updated_input_type_0 import (
        EdgeEvaluateResponseUpdatedInputType0,
    )


T = TypeVar("T", bound="EdgeEvaluateResponse")


@_attrs_define
class EdgeEvaluateResponse:
    """
    Attributes:
        decision (EdgeEvaluateResponseDecision):
        permission_decision (EdgeEvaluateResponsePermissionDecision): Hook-friendly permission decision.
        exit_code (int): Hook/terminal-friendly exit code; zero for allow-like responses and non-zero for deny/block
            guidance.
        reason (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        policy_snapshot (Union[Unset, str]):
        approval_ref (Union[Unset, str]): Opaque approval reference from the Safety Kernel/approval integration. This
            endpoint does not create approval records.
        constraints (Union['EdgeEvaluateResponseConstraintsType0', None, Unset]):
        updated_input (Union['EdgeEvaluateResponseUpdatedInputType0', None, Unset]):
        event_id (Union[Unset, str]): Persisted Edge decision/degraded event ID.
        degraded (Union[Unset, bool]): True when Safety was unavailable and the response follows policy-mode degraded
            behavior.
        error_code (Union[Unset, EdgeEvaluateResponseErrorCode]):
        error_message (Union[Unset, str]): Sanitized bounded error guidance; upstream secret-bearing error strings are
            not returned.
        permission_decision_reason (Union[Unset, str]):
        terminal_title (Union[Unset, str]):
        terminal_message (Union[Unset, str]):
        wait_strategy (Union[Unset, EdgeEvaluateResponseWaitStrategy]):
        timeout_ms (Union[Unset, int]):
    """

    decision: EdgeEvaluateResponseDecision
    permission_decision: EdgeEvaluateResponsePermissionDecision
    exit_code: int
    reason: Union[Unset, str] = UNSET
    rule_id: Union[Unset, str] = UNSET
    policy_snapshot: Union[Unset, str] = UNSET
    approval_ref: Union[Unset, str] = UNSET
    constraints: Union["EdgeEvaluateResponseConstraintsType0", None, Unset] = UNSET
    updated_input: Union["EdgeEvaluateResponseUpdatedInputType0", None, Unset] = UNSET
    event_id: Union[Unset, str] = UNSET
    degraded: Union[Unset, bool] = UNSET
    error_code: Union[Unset, EdgeEvaluateResponseErrorCode] = UNSET
    error_message: Union[Unset, str] = UNSET
    permission_decision_reason: Union[Unset, str] = UNSET
    terminal_title: Union[Unset, str] = UNSET
    terminal_message: Union[Unset, str] = UNSET
    wait_strategy: Union[Unset, EdgeEvaluateResponseWaitStrategy] = UNSET
    timeout_ms: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_evaluate_response_constraints_type_0 import (
            EdgeEvaluateResponseConstraintsType0,
        )
        from ..models.edge_evaluate_response_updated_input_type_0 import (
            EdgeEvaluateResponseUpdatedInputType0,
        )

        decision = self.decision.value

        permission_decision = self.permission_decision.value

        exit_code = self.exit_code

        reason = self.reason

        rule_id = self.rule_id

        policy_snapshot = self.policy_snapshot

        approval_ref = self.approval_ref

        constraints: Union[Dict[str, Any], None, Unset]
        if isinstance(self.constraints, Unset):
            constraints = UNSET
        elif isinstance(self.constraints, EdgeEvaluateResponseConstraintsType0):
            constraints = self.constraints.to_dict()
        else:
            constraints = self.constraints

        updated_input: Union[Dict[str, Any], None, Unset]
        if isinstance(self.updated_input, Unset):
            updated_input = UNSET
        elif isinstance(self.updated_input, EdgeEvaluateResponseUpdatedInputType0):
            updated_input = self.updated_input.to_dict()
        else:
            updated_input = self.updated_input

        event_id = self.event_id

        degraded = self.degraded

        error_code: Union[Unset, str] = UNSET
        if not isinstance(self.error_code, Unset):
            error_code = self.error_code.value

        error_message = self.error_message

        permission_decision_reason = self.permission_decision_reason

        terminal_title = self.terminal_title

        terminal_message = self.terminal_message

        wait_strategy: Union[Unset, str] = UNSET
        if not isinstance(self.wait_strategy, Unset):
            wait_strategy = self.wait_strategy.value

        timeout_ms = self.timeout_ms

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "decision": decision,
                "permission_decision": permission_decision,
                "exit_code": exit_code,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if policy_snapshot is not UNSET:
            field_dict["policy_snapshot"] = policy_snapshot
        if approval_ref is not UNSET:
            field_dict["approval_ref"] = approval_ref
        if constraints is not UNSET:
            field_dict["constraints"] = constraints
        if updated_input is not UNSET:
            field_dict["updated_input"] = updated_input
        if event_id is not UNSET:
            field_dict["event_id"] = event_id
        if degraded is not UNSET:
            field_dict["degraded"] = degraded
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if error_message is not UNSET:
            field_dict["error_message"] = error_message
        if permission_decision_reason is not UNSET:
            field_dict["permission_decision_reason"] = permission_decision_reason
        if terminal_title is not UNSET:
            field_dict["terminal_title"] = terminal_title
        if terminal_message is not UNSET:
            field_dict["terminal_message"] = terminal_message
        if wait_strategy is not UNSET:
            field_dict["wait_strategy"] = wait_strategy
        if timeout_ms is not UNSET:
            field_dict["timeout_ms"] = timeout_ms

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_evaluate_response_constraints_type_0 import (
            EdgeEvaluateResponseConstraintsType0,
        )
        from ..models.edge_evaluate_response_updated_input_type_0 import (
            EdgeEvaluateResponseUpdatedInputType0,
        )

        d = src_dict.copy()
        decision = EdgeEvaluateResponseDecision(d.pop("decision"))

        permission_decision = EdgeEvaluateResponsePermissionDecision(d.pop("permission_decision"))

        exit_code = d.pop("exit_code")

        reason = d.pop("reason", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        policy_snapshot = d.pop("policy_snapshot", UNSET)

        approval_ref = d.pop("approval_ref", UNSET)

        def _parse_constraints(
            data: object,
        ) -> Union["EdgeEvaluateResponseConstraintsType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                constraints_type_0 = EdgeEvaluateResponseConstraintsType0.from_dict(data)

                return constraints_type_0
            except:  # noqa: E722
                pass
            return cast(Union["EdgeEvaluateResponseConstraintsType0", None, Unset], data)

        constraints = _parse_constraints(d.pop("constraints", UNSET))

        def _parse_updated_input(
            data: object,
        ) -> Union["EdgeEvaluateResponseUpdatedInputType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                updated_input_type_0 = EdgeEvaluateResponseUpdatedInputType0.from_dict(data)

                return updated_input_type_0
            except:  # noqa: E722
                pass
            return cast(Union["EdgeEvaluateResponseUpdatedInputType0", None, Unset], data)

        updated_input = _parse_updated_input(d.pop("updated_input", UNSET))

        event_id = d.pop("event_id", UNSET)

        degraded = d.pop("degraded", UNSET)

        _error_code = d.pop("error_code", UNSET)
        error_code: Union[Unset, EdgeEvaluateResponseErrorCode]
        if isinstance(_error_code, Unset):
            error_code = UNSET
        else:
            error_code = EdgeEvaluateResponseErrorCode(_error_code)

        error_message = d.pop("error_message", UNSET)

        permission_decision_reason = d.pop("permission_decision_reason", UNSET)

        terminal_title = d.pop("terminal_title", UNSET)

        terminal_message = d.pop("terminal_message", UNSET)

        _wait_strategy = d.pop("wait_strategy", UNSET)
        wait_strategy: Union[Unset, EdgeEvaluateResponseWaitStrategy]
        if isinstance(_wait_strategy, Unset):
            wait_strategy = UNSET
        else:
            wait_strategy = EdgeEvaluateResponseWaitStrategy(_wait_strategy)

        timeout_ms = d.pop("timeout_ms", UNSET)

        edge_evaluate_response = cls(
            decision=decision,
            permission_decision=permission_decision,
            exit_code=exit_code,
            reason=reason,
            rule_id=rule_id,
            policy_snapshot=policy_snapshot,
            approval_ref=approval_ref,
            constraints=constraints,
            updated_input=updated_input,
            event_id=event_id,
            degraded=degraded,
            error_code=error_code,
            error_message=error_message,
            permission_decision_reason=permission_decision_reason,
            terminal_title=terminal_title,
            terminal_message=terminal_message,
            wait_strategy=wait_strategy,
            timeout_ms=timeout_ms,
        )

        edge_evaluate_response.additional_properties = d
        return edge_evaluate_response

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
