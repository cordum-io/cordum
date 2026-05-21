from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.policy_replay_request_filters import PolicyReplayRequestFilters


T = TypeVar("T", bound="PolicyReplayRequest")


@_attrs_define
class PolicyReplayRequest:
    """
    Attributes:
        from_ (datetime.datetime):
        to (datetime.datetime):
        filters (Union[Unset, PolicyReplayRequestFilters]):
        candidate_bundle_id (Union[Unset, str]):
        candidate_content (Union[Unset, str]):
        use_current_policy (Union[Unset, bool]):
        max_jobs (Union[Unset, int]):
    """

    from_: datetime.datetime
    to: datetime.datetime
    filters: Union[Unset, "PolicyReplayRequestFilters"] = UNSET
    candidate_bundle_id: Union[Unset, str] = UNSET
    candidate_content: Union[Unset, str] = UNSET
    use_current_policy: Union[Unset, bool] = UNSET
    max_jobs: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_replay_request_filters import PolicyReplayRequestFilters

        from_ = self.from_.isoformat()

        to = self.to.isoformat()

        filters: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.filters, Unset):
            filters = self.filters.to_dict()

        candidate_bundle_id = self.candidate_bundle_id

        candidate_content = self.candidate_content

        use_current_policy = self.use_current_policy

        max_jobs = self.max_jobs

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "from": from_,
                "to": to,
            }
        )
        if filters is not UNSET:
            field_dict["filters"] = filters
        if candidate_bundle_id is not UNSET:
            field_dict["candidate_bundle_id"] = candidate_bundle_id
        if candidate_content is not UNSET:
            field_dict["candidate_content"] = candidate_content
        if use_current_policy is not UNSET:
            field_dict["use_current_policy"] = use_current_policy
        if max_jobs is not UNSET:
            field_dict["max_jobs"] = max_jobs

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_replay_request_filters import PolicyReplayRequestFilters

        d = src_dict.copy()
        from_ = isoparse(d.pop("from"))

        to = isoparse(d.pop("to"))

        _filters = d.pop("filters", UNSET)
        filters: Union[Unset, PolicyReplayRequestFilters]
        if isinstance(_filters, Unset):
            filters = UNSET
        else:
            filters = PolicyReplayRequestFilters.from_dict(_filters)

        candidate_bundle_id = d.pop("candidate_bundle_id", UNSET)

        candidate_content = d.pop("candidate_content", UNSET)

        use_current_policy = d.pop("use_current_policy", UNSET)

        max_jobs = d.pop("max_jobs", UNSET)

        policy_replay_request = cls(
            from_=from_,
            to=to,
            filters=filters,
            candidate_bundle_id=candidate_bundle_id,
            candidate_content=candidate_content,
            use_current_policy=use_current_policy,
            max_jobs=max_jobs,
        )

        policy_replay_request.additional_properties = d
        return policy_replay_request

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
