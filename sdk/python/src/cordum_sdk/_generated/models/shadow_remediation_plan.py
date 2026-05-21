from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.shadow_remediation_action_kind import ShadowRemediationActionKind
from ..models.shadow_remediation_plan_audience import ShadowRemediationPlanAudience
from ..models.shadow_remediation_plan_severity import ShadowRemediationPlanSeverity
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.shadow_remediation_step import ShadowRemediationStep


T = TypeVar("T", bound="ShadowRemediationPlan")


@_attrs_define
class ShadowRemediationPlan:
    """Deterministic advisory remediation plan. All commands inside steps use literal placeholders and never carry live
    secrets or developer paths.

        Attributes:
            audience (ShadowRemediationPlanAudience):
            severity (ShadowRemediationPlanSeverity):
            action_kind (ShadowRemediationActionKind):
            summary (str):
            risk_explanation (str):
            recommended_action (str):
            steps (List['ShadowRemediationStep']):
            generator_version (str):
            generated_at (datetime.datetime):
            advisory_only (bool): Always true in this generator. Reserved field — a future enforcement mode (out of scope)
                may flip it without changing the type signature.
            finding_id (Union[Unset, str]): Empty when generated from a scanner-shape Finding (no persistent ID).
            tenant_id (Union[Unset, str]):
            safety_notes (Union[Unset, List[str]]):
    """

    audience: ShadowRemediationPlanAudience
    severity: ShadowRemediationPlanSeverity
    action_kind: ShadowRemediationActionKind
    summary: str
    risk_explanation: str
    recommended_action: str
    steps: List["ShadowRemediationStep"]
    generator_version: str
    generated_at: datetime.datetime
    advisory_only: bool
    finding_id: Union[Unset, str] = UNSET
    tenant_id: Union[Unset, str] = UNSET
    safety_notes: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.shadow_remediation_step import ShadowRemediationStep

        audience = self.audience.value

        severity = self.severity.value

        action_kind = self.action_kind.value

        summary = self.summary

        risk_explanation = self.risk_explanation

        recommended_action = self.recommended_action

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        generator_version = self.generator_version

        generated_at = self.generated_at.isoformat()

        advisory_only = self.advisory_only

        finding_id = self.finding_id

        tenant_id = self.tenant_id

        safety_notes: Union[Unset, List[str]] = UNSET
        if not isinstance(self.safety_notes, Unset):
            safety_notes = self.safety_notes

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "audience": audience,
                "severity": severity,
                "action_kind": action_kind,
                "summary": summary,
                "risk_explanation": risk_explanation,
                "recommended_action": recommended_action,
                "steps": steps,
                "generator_version": generator_version,
                "generated_at": generated_at,
                "advisory_only": advisory_only,
            }
        )
        if finding_id is not UNSET:
            field_dict["finding_id"] = finding_id
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if safety_notes is not UNSET:
            field_dict["safety_notes"] = safety_notes

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.shadow_remediation_step import ShadowRemediationStep

        d = src_dict.copy()
        audience = ShadowRemediationPlanAudience(d.pop("audience"))

        severity = ShadowRemediationPlanSeverity(d.pop("severity"))

        action_kind = ShadowRemediationActionKind(d.pop("action_kind"))

        summary = d.pop("summary")

        risk_explanation = d.pop("risk_explanation")

        recommended_action = d.pop("recommended_action")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = ShadowRemediationStep.from_dict(steps_item_data)

            steps.append(steps_item)

        generator_version = d.pop("generator_version")

        generated_at = isoparse(d.pop("generated_at"))

        advisory_only = d.pop("advisory_only")

        finding_id = d.pop("finding_id", UNSET)

        tenant_id = d.pop("tenant_id", UNSET)

        safety_notes = cast(List[str], d.pop("safety_notes", UNSET))

        shadow_remediation_plan = cls(
            audience=audience,
            severity=severity,
            action_kind=action_kind,
            summary=summary,
            risk_explanation=risk_explanation,
            recommended_action=recommended_action,
            steps=steps,
            generator_version=generator_version,
            generated_at=generated_at,
            advisory_only=advisory_only,
            finding_id=finding_id,
            tenant_id=tenant_id,
            safety_notes=safety_notes,
        )

        shadow_remediation_plan.additional_properties = d
        return shadow_remediation_plan

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
