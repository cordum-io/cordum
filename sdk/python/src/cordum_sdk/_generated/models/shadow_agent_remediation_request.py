from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.shadow_agent_remediation_request_audience import ShadowAgentRemediationRequestAudience
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="ShadowAgentRemediationRequest")


@_attrs_define
class ShadowAgentRemediationRequest:
    """Optional body for generating a remediation plan. Both fields default to the generator's documented defaults when
    omitted.

        Attributes:
            audience (Union[Unset, ShadowAgentRemediationRequestAudience]): Selects wording + step layering. Defaults to
                "both" when omitted.
            omit_commands (Union[Unset, bool]): When true, strips the Command and APIRequest.Body fields from emitted steps.
                Defaults to false.
    """

    audience: Union[Unset, ShadowAgentRemediationRequestAudience] = UNSET
    omit_commands: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        audience: Union[Unset, str] = UNSET
        if not isinstance(self.audience, Unset):
            audience = self.audience.value

        omit_commands = self.omit_commands

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if audience is not UNSET:
            field_dict["audience"] = audience
        if omit_commands is not UNSET:
            field_dict["omit_commands"] = omit_commands

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        _audience = d.pop("audience", UNSET)
        audience: Union[Unset, ShadowAgentRemediationRequestAudience]
        if isinstance(_audience, Unset):
            audience = UNSET
        else:
            audience = ShadowAgentRemediationRequestAudience(_audience)

        omit_commands = d.pop("omit_commands", UNSET)

        shadow_agent_remediation_request = cls(
            audience=audience,
            omit_commands=omit_commands,
        )

        shadow_agent_remediation_request.additional_properties = d
        return shadow_agent_remediation_request

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
