from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import Dict

if TYPE_CHECKING:
    from ..models.edge_session import EdgeSession
    from ..models.edge_agent_execution import EdgeAgentExecution


T = TypeVar("T", bound="EdgeSessionCreateResponse")


@_attrs_define
class EdgeSessionCreateResponse:
    """
    Attributes:
        session_id (str):
        execution_id (str):
        trace_id (str):
        policy_snapshot (str): Redacted policy snapshot identifier or summary; raw secrets are redacted before
            persistence/response.
        dashboard_url (str): Relative dashboard URL for the Edge session.
        session (EdgeSession):
        execution (EdgeAgentExecution):
    """

    session_id: str
    execution_id: str
    trace_id: str
    policy_snapshot: str
    dashboard_url: str
    session: "EdgeSession"
    execution: "EdgeAgentExecution"
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_session import EdgeSession
        from ..models.edge_agent_execution import EdgeAgentExecution

        session_id = self.session_id

        execution_id = self.execution_id

        trace_id = self.trace_id

        policy_snapshot = self.policy_snapshot

        dashboard_url = self.dashboard_url

        session = self.session.to_dict()

        execution = self.execution.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session_id": session_id,
                "execution_id": execution_id,
                "trace_id": trace_id,
                "policy_snapshot": policy_snapshot,
                "dashboard_url": dashboard_url,
                "session": session,
                "execution": execution,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_session import EdgeSession
        from ..models.edge_agent_execution import EdgeAgentExecution

        d = src_dict.copy()
        session_id = d.pop("session_id")

        execution_id = d.pop("execution_id")

        trace_id = d.pop("trace_id")

        policy_snapshot = d.pop("policy_snapshot")

        dashboard_url = d.pop("dashboard_url")

        session = EdgeSession.from_dict(d.pop("session"))

        execution = EdgeAgentExecution.from_dict(d.pop("execution"))

        edge_session_create_response = cls(
            session_id=session_id,
            execution_id=execution_id,
            trace_id=trace_id,
            policy_snapshot=policy_snapshot,
            dashboard_url=dashboard_url,
            session=session,
            execution=execution,
        )

        edge_session_create_response.additional_properties = d
        return edge_session_create_response

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
