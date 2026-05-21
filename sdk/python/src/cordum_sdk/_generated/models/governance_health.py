from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.governance_health_grade import GovernanceHealthGrade
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.governance_health_factors import GovernanceHealthFactors


T = TypeVar("T", bound="GovernanceHealth")


@_attrs_define
class GovernanceHealth:
    """
    Attributes:
        score (int):
        grade (GovernanceHealthGrade):
        generated_at (datetime.datetime):
        factors (GovernanceHealthFactors):
        truncated_at_max (Union[Unset, bool]):
    """

    score: int
    grade: GovernanceHealthGrade
    generated_at: datetime.datetime
    factors: "GovernanceHealthFactors"
    truncated_at_max: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.governance_health_factors import GovernanceHealthFactors

        score = self.score

        grade = self.grade.value

        generated_at = self.generated_at.isoformat()

        factors = self.factors.to_dict()

        truncated_at_max = self.truncated_at_max

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "score": score,
                "grade": grade,
                "generated_at": generated_at,
                "factors": factors,
            }
        )
        if truncated_at_max is not UNSET:
            field_dict["truncated_at_max"] = truncated_at_max

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.governance_health_factors import GovernanceHealthFactors

        d = src_dict.copy()
        score = d.pop("score")

        grade = GovernanceHealthGrade(d.pop("grade"))

        generated_at = isoparse(d.pop("generated_at"))

        factors = GovernanceHealthFactors.from_dict(d.pop("factors"))

        truncated_at_max = d.pop("truncated_at_max", UNSET)

        governance_health = cls(
            score=score,
            grade=grade,
            generated_at=generated_at,
            factors=factors,
            truncated_at_max=truncated_at_max,
        )

        governance_health.additional_properties = d
        return governance_health

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
