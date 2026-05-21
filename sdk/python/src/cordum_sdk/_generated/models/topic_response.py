from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="TopicResponse")


@_attrs_define
class TopicResponse:
    """
    Attributes:
        name (str):
        status (str):
        pool (Union[Unset, str]):
        input_schema_id (Union[Unset, str]):
        output_schema_id (Union[Unset, str]):
        pack_id (Union[Unset, str]):
        requires (Union[Unset, List[str]]):
        risk_tags (Union[Unset, List[str]]):
        active_worker_count (Union[Unset, int]):
    """

    name: str
    status: str
    pool: Union[Unset, str] = UNSET
    input_schema_id: Union[Unset, str] = UNSET
    output_schema_id: Union[Unset, str] = UNSET
    pack_id: Union[Unset, str] = UNSET
    requires: Union[Unset, List[str]] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    active_worker_count: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        status = self.status

        pool = self.pool

        input_schema_id = self.input_schema_id

        output_schema_id = self.output_schema_id

        pack_id = self.pack_id

        requires: Union[Unset, List[str]] = UNSET
        if not isinstance(self.requires, Unset):
            requires = self.requires

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags

        active_worker_count = self.active_worker_count

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "status": status,
            }
        )
        if pool is not UNSET:
            field_dict["pool"] = pool
        if input_schema_id is not UNSET:
            field_dict["input_schema_id"] = input_schema_id
        if output_schema_id is not UNSET:
            field_dict["output_schema_id"] = output_schema_id
        if pack_id is not UNSET:
            field_dict["pack_id"] = pack_id
        if requires is not UNSET:
            field_dict["requires"] = requires
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags
        if active_worker_count is not UNSET:
            field_dict["active_worker_count"] = active_worker_count

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name")

        status = d.pop("status")

        pool = d.pop("pool", UNSET)

        input_schema_id = d.pop("input_schema_id", UNSET)

        output_schema_id = d.pop("output_schema_id", UNSET)

        pack_id = d.pop("pack_id", UNSET)

        requires = cast(List[str], d.pop("requires", UNSET))

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))

        active_worker_count = d.pop("active_worker_count", UNSET)

        topic_response = cls(
            name=name,
            status=status,
            pool=pool,
            input_schema_id=input_schema_id,
            output_schema_id=output_schema_id,
            pack_id=pack_id,
            requires=requires,
            risk_tags=risk_tags,
            active_worker_count=active_worker_count,
        )

        topic_response.additional_properties = d
        return topic_response

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
