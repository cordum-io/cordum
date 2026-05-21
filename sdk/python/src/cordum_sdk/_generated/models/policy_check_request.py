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
    from ..models.policy_check_request_labels import PolicyCheckRequestLabels
    from ..models.policy_check_request_context import PolicyCheckRequestContext


T = TypeVar("T", bound="PolicyCheckRequest")


@_attrs_define
class PolicyCheckRequest:
    """
    Attributes:
        job_id (Union[Unset, str]):
        topic (Union[Unset, str]):
        tenant (Union[Unset, str]):
        org_id (Union[Unset, str]):
        team_id (Union[Unset, str]):
        capability (Union[Unset, str]):
        risk_tags (Union[Unset, List[str]]):
        context (Union[Unset, PolicyCheckRequestContext]):
        labels (Union[Unset, PolicyCheckRequestLabels]):
    """

    job_id: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    tenant: Union[Unset, str] = UNSET
    org_id: Union[Unset, str] = UNSET
    team_id: Union[Unset, str] = UNSET
    capability: Union[Unset, str] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    context: Union[Unset, "PolicyCheckRequestContext"] = UNSET
    labels: Union[Unset, "PolicyCheckRequestLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_check_request_labels import PolicyCheckRequestLabels
        from ..models.policy_check_request_context import PolicyCheckRequestContext

        job_id = self.job_id

        topic = self.topic

        tenant = self.tenant

        org_id = self.org_id

        team_id = self.team_id

        capability = self.capability

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags

        context: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.context, Unset):
            context = self.context.to_dict()

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if topic is not UNSET:
            field_dict["topic"] = topic
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if team_id is not UNSET:
            field_dict["team_id"] = team_id
        if capability is not UNSET:
            field_dict["capability"] = capability
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags
        if context is not UNSET:
            field_dict["context"] = context
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_check_request_labels import PolicyCheckRequestLabels
        from ..models.policy_check_request_context import PolicyCheckRequestContext

        d = src_dict.copy()
        job_id = d.pop("job_id", UNSET)

        topic = d.pop("topic", UNSET)

        tenant = d.pop("tenant", UNSET)

        org_id = d.pop("org_id", UNSET)

        team_id = d.pop("team_id", UNSET)

        capability = d.pop("capability", UNSET)

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))

        _context = d.pop("context", UNSET)
        context: Union[Unset, PolicyCheckRequestContext]
        if isinstance(_context, Unset):
            context = UNSET
        else:
            context = PolicyCheckRequestContext.from_dict(_context)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, PolicyCheckRequestLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = PolicyCheckRequestLabels.from_dict(_labels)

        policy_check_request = cls(
            job_id=job_id,
            topic=topic,
            tenant=tenant,
            org_id=org_id,
            team_id=team_id,
            capability=capability,
            risk_tags=risk_tags,
            context=context,
            labels=labels,
        )

        policy_check_request.additional_properties = d
        return policy_check_request

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
