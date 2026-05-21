from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.policy_bundle_summary import PolicyBundleSummary


T = TypeVar("T", bound="ListPolicyBundlesResponse200")


@_attrs_define
class ListPolicyBundlesResponse200:
    """
    Attributes:
        bundles (Union[Unset, List['PolicyBundleSummary']]):
        items (Union[Unset, List['PolicyBundleSummary']]):
        updated_at (Union[Unset, datetime.datetime]):
    """

    bundles: Union[Unset, List["PolicyBundleSummary"]] = UNSET
    items: Union[Unset, List["PolicyBundleSummary"]] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_bundle_summary import PolicyBundleSummary

        bundles: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.bundles, Unset):
            bundles = []
            for bundles_item_data in self.bundles:
                bundles_item = bundles_item_data.to_dict()
                bundles.append(bundles_item)

        items: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.items, Unset):
            items = []
            for items_item_data in self.items:
                items_item = items_item_data.to_dict()
                items.append(items_item)

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if bundles is not UNSET:
            field_dict["bundles"] = bundles
        if items is not UNSET:
            field_dict["items"] = items
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_bundle_summary import PolicyBundleSummary

        d = src_dict.copy()
        bundles = []
        _bundles = d.pop("bundles", UNSET)
        for bundles_item_data in _bundles or []:
            bundles_item = PolicyBundleSummary.from_dict(bundles_item_data)

            bundles.append(bundles_item)

        items = []
        _items = d.pop("items", UNSET)
        for items_item_data in _items or []:
            items_item = PolicyBundleSummary.from_dict(items_item_data)

            items.append(items_item)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        list_policy_bundles_response_200 = cls(
            bundles=bundles,
            items=items,
            updated_at=updated_at,
        )

        list_policy_bundles_response_200.additional_properties = d
        return list_policy_bundles_response_200

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
