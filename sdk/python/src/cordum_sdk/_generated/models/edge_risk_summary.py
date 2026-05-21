from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_risk_summary_max_risk import EdgeRiskSummaryMaxRisk
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EdgeRiskSummary")


@_attrs_define
class EdgeRiskSummary:
    """
    Attributes:
        denied_count (Union[Unset, int]):
        approval_count (Union[Unset, int]):
        artifact_count (Union[Unset, int]):
        max_risk (Union[Unset, EdgeRiskSummaryMaxRisk]):
    """

    denied_count: Union[Unset, int] = UNSET
    approval_count: Union[Unset, int] = UNSET
    artifact_count: Union[Unset, int] = UNSET
    max_risk: Union[Unset, EdgeRiskSummaryMaxRisk] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        denied_count = self.denied_count

        approval_count = self.approval_count

        artifact_count = self.artifact_count

        max_risk: Union[Unset, str] = UNSET
        if not isinstance(self.max_risk, Unset):
            max_risk = self.max_risk.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if denied_count is not UNSET:
            field_dict["denied_count"] = denied_count
        if approval_count is not UNSET:
            field_dict["approval_count"] = approval_count
        if artifact_count is not UNSET:
            field_dict["artifact_count"] = artifact_count
        if max_risk is not UNSET:
            field_dict["max_risk"] = max_risk

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        denied_count = d.pop("denied_count", UNSET)

        approval_count = d.pop("approval_count", UNSET)

        artifact_count = d.pop("artifact_count", UNSET)

        _max_risk = d.pop("max_risk", UNSET)
        max_risk: Union[Unset, EdgeRiskSummaryMaxRisk]
        if isinstance(_max_risk, Unset):
            max_risk = UNSET
        else:
            max_risk = EdgeRiskSummaryMaxRisk(_max_risk)

        edge_risk_summary = cls(
            denied_count=denied_count,
            approval_count=approval_count,
            artifact_count=artifact_count,
            max_risk=max_risk,
        )

        edge_risk_summary.additional_properties = d
        return edge_risk_summary

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
