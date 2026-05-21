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
    from ..models.marketplace_pack import MarketplacePack
    from ..models.marketplace_catalog_catalogs_item import MarketplaceCatalogCatalogsItem


T = TypeVar("T", bound="MarketplaceCatalog")


@_attrs_define
class MarketplaceCatalog:
    """
    Attributes:
        catalogs (Union[Unset, List['MarketplaceCatalogCatalogsItem']]):
        items (Union[Unset, List['MarketplacePack']]):
        fetched_at (Union[Unset, datetime.datetime]):
        cached (Union[Unset, bool]):
    """

    catalogs: Union[Unset, List["MarketplaceCatalogCatalogsItem"]] = UNSET
    items: Union[Unset, List["MarketplacePack"]] = UNSET
    fetched_at: Union[Unset, datetime.datetime] = UNSET
    cached: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.marketplace_pack import MarketplacePack
        from ..models.marketplace_catalog_catalogs_item import MarketplaceCatalogCatalogsItem

        catalogs: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.catalogs, Unset):
            catalogs = []
            for catalogs_item_data in self.catalogs:
                catalogs_item = catalogs_item_data.to_dict()
                catalogs.append(catalogs_item)

        items: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.items, Unset):
            items = []
            for items_item_data in self.items:
                items_item = items_item_data.to_dict()
                items.append(items_item)

        fetched_at: Union[Unset, str] = UNSET
        if not isinstance(self.fetched_at, Unset):
            fetched_at = self.fetched_at.isoformat()

        cached = self.cached

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if catalogs is not UNSET:
            field_dict["catalogs"] = catalogs
        if items is not UNSET:
            field_dict["items"] = items
        if fetched_at is not UNSET:
            field_dict["fetched_at"] = fetched_at
        if cached is not UNSET:
            field_dict["cached"] = cached

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.marketplace_pack import MarketplacePack
        from ..models.marketplace_catalog_catalogs_item import MarketplaceCatalogCatalogsItem

        d = src_dict.copy()
        catalogs = []
        _catalogs = d.pop("catalogs", UNSET)
        for catalogs_item_data in _catalogs or []:
            catalogs_item = MarketplaceCatalogCatalogsItem.from_dict(catalogs_item_data)

            catalogs.append(catalogs_item)

        items = []
        _items = d.pop("items", UNSET)
        for items_item_data in _items or []:
            items_item = MarketplacePack.from_dict(items_item_data)

            items.append(items_item)

        _fetched_at = d.pop("fetched_at", UNSET)
        fetched_at: Union[Unset, datetime.datetime]
        if isinstance(_fetched_at, Unset):
            fetched_at = UNSET
        else:
            fetched_at = isoparse(_fetched_at)

        cached = d.pop("cached", UNSET)

        marketplace_catalog = cls(
            catalogs=catalogs,
            items=items,
            fetched_at=fetched_at,
            cached=cached,
        )

        marketplace_catalog.additional_properties = d
        return marketplace_catalog

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
