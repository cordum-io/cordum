from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import Dict

if TYPE_CHECKING:
    from ..models.governance_health_factor import GovernanceHealthFactor


T = TypeVar("T", bound="GovernanceHealthFactors")


@_attrs_define
class GovernanceHealthFactors:
    """ """

    additional_properties: Dict[str, "GovernanceHealthFactor"] = _attrs_field(
        init=False, factory=dict
    )

    def to_dict(self) -> Dict[str, Any]:
        from ..models.governance_health_factor import GovernanceHealthFactor

        field_dict: Dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.governance_health_factor import GovernanceHealthFactor

        d = src_dict.copy()
        governance_health_factors = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = GovernanceHealthFactor.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        governance_health_factors.additional_properties = additional_properties
        return governance_health_factors

    @property
    def additional_keys(self) -> List[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> "GovernanceHealthFactor":
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: "GovernanceHealthFactor") -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
