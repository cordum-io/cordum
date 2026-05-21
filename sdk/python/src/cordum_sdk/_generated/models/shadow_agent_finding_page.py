from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.shadow_agent_finding import ShadowAgentFinding


T = TypeVar("T", bound="ShadowAgentFindingPage")


@_attrs_define
class ShadowAgentFindingPage:
    """
    Attributes:
        findings (Union[Unset, List['ShadowAgentFinding']]):
        next_cursor (Union[None, Unset, str]):
    """

    findings: Union[Unset, List["ShadowAgentFinding"]] = UNSET
    next_cursor: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.shadow_agent_finding import ShadowAgentFinding

        findings: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.findings, Unset):
            findings = []
            for findings_item_data in self.findings:
                findings_item = findings_item_data.to_dict()
                findings.append(findings_item)

        next_cursor: Union[None, Unset, str]
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if findings is not UNSET:
            field_dict["findings"] = findings
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.shadow_agent_finding import ShadowAgentFinding

        d = src_dict.copy()
        findings = []
        _findings = d.pop("findings", UNSET)
        for findings_item_data in _findings or []:
            findings_item = ShadowAgentFinding.from_dict(findings_item_data)

            findings.append(findings_item)

        def _parse_next_cursor(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        next_cursor = _parse_next_cursor(d.pop("next_cursor", UNSET))

        shadow_agent_finding_page = cls(
            findings=findings,
            next_cursor=next_cursor,
        )

        shadow_agent_finding_page.additional_properties = d
        return shadow_agent_finding_page

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
