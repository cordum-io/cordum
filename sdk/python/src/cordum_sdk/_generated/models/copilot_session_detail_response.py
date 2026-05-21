from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import cast, List
from typing import Dict

if TYPE_CHECKING:
    from ..models.copilot_session import CopilotSession
    from ..models.copilot_session_decision import CopilotSessionDecision
    from ..models.copilot_session_job import CopilotSessionJob


T = TypeVar("T", bound="CopilotSessionDetailResponse")


@_attrs_define
class CopilotSessionDetailResponse:
    """
    Attributes:
        session (CopilotSession):
        jobs (List['CopilotSessionJob']):
        decisions (List['CopilotSessionDecision']):
        truncated (bool): True when the backend capped jobs or decisions at 500 entries.
    """

    session: "CopilotSession"
    jobs: List["CopilotSessionJob"]
    decisions: List["CopilotSessionDecision"]
    truncated: bool
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.copilot_session import CopilotSession
        from ..models.copilot_session_decision import CopilotSessionDecision
        from ..models.copilot_session_job import CopilotSessionJob

        session = self.session.to_dict()

        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        decisions = []
        for decisions_item_data in self.decisions:
            decisions_item = decisions_item_data.to_dict()
            decisions.append(decisions_item)

        truncated = self.truncated

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session": session,
                "jobs": jobs,
                "decisions": decisions,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.copilot_session import CopilotSession
        from ..models.copilot_session_decision import CopilotSessionDecision
        from ..models.copilot_session_job import CopilotSessionJob

        d = src_dict.copy()
        session = CopilotSession.from_dict(d.pop("session"))

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = CopilotSessionJob.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        decisions = []
        _decisions = d.pop("decisions")
        for decisions_item_data in _decisions:
            decisions_item = CopilotSessionDecision.from_dict(decisions_item_data)

            decisions.append(decisions_item)

        truncated = d.pop("truncated")

        copilot_session_detail_response = cls(
            session=session,
            jobs=jobs,
            decisions=decisions,
            truncated=truncated,
        )

        copilot_session_detail_response.additional_properties = d
        return copilot_session_detail_response

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
