from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="MarketplacePack")


@_attrs_define
class MarketplacePack:
    """
    Attributes:
        catalog_id (Union[Unset, str]):
        pack_id (Union[Unset, str]):
        name (Union[Unset, str]):
        version (Union[Unset, str]):
        description (Union[Unset, str]):
        author (Union[Unset, str]):
        sha256 (Union[Unset, str]):
        url (Union[Unset, str]):
    """

    catalog_id: Union[Unset, str] = UNSET
    pack_id: Union[Unset, str] = UNSET
    name: Union[Unset, str] = UNSET
    version: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    author: Union[Unset, str] = UNSET
    sha256: Union[Unset, str] = UNSET
    url: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        catalog_id = self.catalog_id

        pack_id = self.pack_id

        name = self.name

        version = self.version

        description = self.description

        author = self.author

        sha256 = self.sha256

        url = self.url

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if catalog_id is not UNSET:
            field_dict["catalog_id"] = catalog_id
        if pack_id is not UNSET:
            field_dict["pack_id"] = pack_id
        if name is not UNSET:
            field_dict["name"] = name
        if version is not UNSET:
            field_dict["version"] = version
        if description is not UNSET:
            field_dict["description"] = description
        if author is not UNSET:
            field_dict["author"] = author
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256
        if url is not UNSET:
            field_dict["url"] = url

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        catalog_id = d.pop("catalog_id", UNSET)

        pack_id = d.pop("pack_id", UNSET)

        name = d.pop("name", UNSET)

        version = d.pop("version", UNSET)

        description = d.pop("description", UNSET)

        author = d.pop("author", UNSET)

        sha256 = d.pop("sha256", UNSET)

        url = d.pop("url", UNSET)

        marketplace_pack = cls(
            catalog_id=catalog_id,
            pack_id=pack_id,
            name=name,
            version=version,
            description=description,
            author=author,
            sha256=sha256,
            url=url,
        )

        marketplace_pack.additional_properties = d
        return marketplace_pack

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
