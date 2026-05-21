from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.approval_analytics_response_window import ApprovalAnalyticsResponseWindow
    from ..models.approval_analytics_group import ApprovalAnalyticsGroup
    from ..models.approval_analytics_summary import ApprovalAnalyticsSummary


T = TypeVar("T", bound="ApprovalAnalyticsResponse")


@_attrs_define
class ApprovalAnalyticsResponse:
    """
    Attributes:
        window (ApprovalAnalyticsResponseWindow):
        summary (ApprovalAnalyticsSummary):
        groups (Union[Unset, List['ApprovalAnalyticsGroup']]):
    """

    window: "ApprovalAnalyticsResponseWindow"
    summary: "ApprovalAnalyticsSummary"
    groups: Union[Unset, List["ApprovalAnalyticsGroup"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.approval_analytics_response_window import ApprovalAnalyticsResponseWindow
        from ..models.approval_analytics_group import ApprovalAnalyticsGroup
        from ..models.approval_analytics_summary import ApprovalAnalyticsSummary

        window = self.window.to_dict()

        summary = self.summary.to_dict()

        groups: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.groups, Unset):
            groups = []
            for groups_item_data in self.groups:
                groups_item = groups_item_data.to_dict()
                groups.append(groups_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window": window,
                "summary": summary,
            }
        )
        if groups is not UNSET:
            field_dict["groups"] = groups

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.approval_analytics_response_window import ApprovalAnalyticsResponseWindow
        from ..models.approval_analytics_group import ApprovalAnalyticsGroup
        from ..models.approval_analytics_summary import ApprovalAnalyticsSummary

        d = src_dict.copy()
        window = ApprovalAnalyticsResponseWindow.from_dict(d.pop("window"))

        summary = ApprovalAnalyticsSummary.from_dict(d.pop("summary"))

        groups = []
        _groups = d.pop("groups", UNSET)
        for groups_item_data in _groups or []:
            groups_item = ApprovalAnalyticsGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        approval_analytics_response = cls(
            window=window,
            summary=summary,
            groups=groups,
        )

        approval_analytics_response.additional_properties = d
        return approval_analytics_response

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
