from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset


T = TypeVar("T", bound="EdgeHeartbeatResponse")


@_attrs_define
class EdgeHeartbeatResponse:
    """
    Attributes:
        session_id (str):
        heartbeat_alive (bool):
    """

    session_id: str
    heartbeat_alive: bool
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        session_id = self.session_id

        heartbeat_alive = self.heartbeat_alive

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session_id": session_id,
                "heartbeat_alive": heartbeat_alive,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        session_id = d.pop("session_id")

        heartbeat_alive = d.pop("heartbeat_alive")

        edge_heartbeat_response = cls(
            session_id=session_id,
            heartbeat_alive=heartbeat_alive,
        )

        edge_heartbeat_response.additional_properties = d
        return edge_heartbeat_response

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
