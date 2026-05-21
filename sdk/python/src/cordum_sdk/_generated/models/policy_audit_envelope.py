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
    from ..models.policy_audit_entry import PolicyAuditEntry


T = TypeVar("T", bound="PolicyAuditEnvelope")


@_attrs_define
class PolicyAuditEnvelope:
    """Paginated envelope returned by `GET /api/v1/policy/audit`. The
    `type=output` special path on the same endpoint returns only the
    `items` field; consumers should treat `total` / `has_more` /
    `offset` as optional when filtering by that type.

        Attributes:
            items (List['PolicyAuditEntry']):
            total (Union[Unset, int]): Total entries matching the filter (pre-pagination).
            has_more (Union[Unset, bool]): True when more entries are available past the current window.
            offset (Union[Unset, int]): Echo of the requested offset (default 0).
    """

    items: List["PolicyAuditEntry"]
    total: Union[Unset, int] = UNSET
    has_more: Union[Unset, bool] = UNSET
    offset: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_audit_entry import PolicyAuditEntry

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        total = self.total

        has_more = self.has_more

        offset = self.offset

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
            }
        )
        if total is not UNSET:
            field_dict["total"] = total
        if has_more is not UNSET:
            field_dict["has_more"] = has_more
        if offset is not UNSET:
            field_dict["offset"] = offset

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_audit_entry import PolicyAuditEntry

        d = src_dict.copy()
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = PolicyAuditEntry.from_dict(items_item_data)

            items.append(items_item)

        total = d.pop("total", UNSET)

        has_more = d.pop("has_more", UNSET)

        offset = d.pop("offset", UNSET)

        policy_audit_envelope = cls(
            items=items,
            total=total,
            has_more=has_more,
            offset=offset,
        )

        policy_audit_envelope.additional_properties = d
        return policy_audit_envelope

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
