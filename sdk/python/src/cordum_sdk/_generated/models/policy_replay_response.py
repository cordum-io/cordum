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
from uuid import UUID

if TYPE_CHECKING:
  from ..models.policy_replay_response_rule_hits_item import PolicyReplayResponseRuleHitsItem
  from ..models.policy_replay_response_summary import PolicyReplayResponseSummary
  from ..models.policy_replay_response_changes_item import PolicyReplayResponseChangesItem
  from ..models.policy_replay_response_time_range import PolicyReplayResponseTimeRange





T = TypeVar("T", bound="PolicyReplayResponse")


@_attrs_define
class PolicyReplayResponse:
    """ 
        Attributes:
            replay_id (UUID):
            policy_snapshot (str):
            time_range (PolicyReplayResponseTimeRange):
            summary (PolicyReplayResponseSummary):
            rule_hits (List['PolicyReplayResponseRuleHitsItem']):
            changes (List['PolicyReplayResponseChangesItem']):
            warnings (Union[Unset, List[str]]):
            errors (Union[Unset, List[str]]):
     """

    replay_id: UUID
    policy_snapshot: str
    time_range: 'PolicyReplayResponseTimeRange'
    summary: 'PolicyReplayResponseSummary'
    rule_hits: List['PolicyReplayResponseRuleHitsItem']
    changes: List['PolicyReplayResponseChangesItem']
    warnings: Union[Unset, List[str]] = UNSET
    errors: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_replay_response_rule_hits_item import PolicyReplayResponseRuleHitsItem
        from ..models.policy_replay_response_summary import PolicyReplayResponseSummary
        from ..models.policy_replay_response_changes_item import PolicyReplayResponseChangesItem
        from ..models.policy_replay_response_time_range import PolicyReplayResponseTimeRange
        replay_id = str(self.replay_id)

        policy_snapshot = self.policy_snapshot

        time_range = self.time_range.to_dict()

        summary = self.summary.to_dict()

        rule_hits = []
        for rule_hits_item_data in self.rule_hits:
            rule_hits_item = rule_hits_item_data.to_dict()
            rule_hits.append(rule_hits_item)



        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)



        warnings: Union[Unset, List[str]] = UNSET
        if not isinstance(self.warnings, Unset):
            warnings = self.warnings



        errors: Union[Unset, List[str]] = UNSET
        if not isinstance(self.errors, Unset):
            errors = self.errors




        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "replay_id": replay_id,
            "policy_snapshot": policy_snapshot,
            "time_range": time_range,
            "summary": summary,
            "rule_hits": rule_hits,
            "changes": changes,
        })
        if warnings is not UNSET:
            field_dict["warnings"] = warnings
        if errors is not UNSET:
            field_dict["errors"] = errors

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_replay_response_rule_hits_item import PolicyReplayResponseRuleHitsItem
        from ..models.policy_replay_response_summary import PolicyReplayResponseSummary
        from ..models.policy_replay_response_changes_item import PolicyReplayResponseChangesItem
        from ..models.policy_replay_response_time_range import PolicyReplayResponseTimeRange
        d = src_dict.copy()
        replay_id = UUID(d.pop("replay_id"))




        policy_snapshot = d.pop("policy_snapshot")

        time_range = PolicyReplayResponseTimeRange.from_dict(d.pop("time_range"))




        summary = PolicyReplayResponseSummary.from_dict(d.pop("summary"))




        rule_hits = []
        _rule_hits = d.pop("rule_hits")
        for rule_hits_item_data in (_rule_hits):
            rule_hits_item = PolicyReplayResponseRuleHitsItem.from_dict(rule_hits_item_data)



            rule_hits.append(rule_hits_item)


        changes = []
        _changes = d.pop("changes")
        for changes_item_data in (_changes):
            changes_item = PolicyReplayResponseChangesItem.from_dict(changes_item_data)



            changes.append(changes_item)


        warnings = cast(List[str], d.pop("warnings", UNSET))


        errors = cast(List[str], d.pop("errors", UNSET))


        policy_replay_response = cls(
            replay_id=replay_id,
            policy_snapshot=policy_snapshot,
            time_range=time_range,
            summary=summary,
            rule_hits=rule_hits,
            changes=changes,
            warnings=warnings,
            errors=errors,
        )


        policy_replay_response.additional_properties = d
        return policy_replay_response

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
