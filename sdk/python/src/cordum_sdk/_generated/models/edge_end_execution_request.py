from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_end_execution_request_status import EdgeEndExecutionRequestStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="EdgeEndExecutionRequest")


@_attrs_define
class EdgeEndExecutionRequest:
    """
    Attributes:
        status (Union[Unset, EdgeEndExecutionRequestStatus]):  Default: EdgeEndExecutionRequestStatus.SUCCEEDED.
        ended_at (Union[Unset, datetime.datetime]):
    """

    status: Union[Unset, EdgeEndExecutionRequestStatus] = EdgeEndExecutionRequestStatus.SUCCEEDED
    ended_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        status: Union[Unset, str] = UNSET
        if not isinstance(self.status, Unset):
            status = self.status.value

        ended_at: Union[Unset, str] = UNSET
        if not isinstance(self.ended_at, Unset):
            ended_at = self.ended_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if status is not UNSET:
            field_dict["status"] = status
        if ended_at is not UNSET:
            field_dict["ended_at"] = ended_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        _status = d.pop("status", UNSET)
        status: Union[Unset, EdgeEndExecutionRequestStatus]
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = EdgeEndExecutionRequestStatus(_status)

        _ended_at = d.pop("ended_at", UNSET)
        ended_at: Union[Unset, datetime.datetime]
        if isinstance(_ended_at, Unset):
            ended_at = UNSET
        else:
            ended_at = isoparse(_ended_at)

        edge_end_execution_request = cls(
            status=status,
            ended_at=ended_at,
        )

        edge_end_execution_request.additional_properties = d
        return edge_end_execution_request

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
