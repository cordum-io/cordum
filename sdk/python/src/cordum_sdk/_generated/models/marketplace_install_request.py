from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="MarketplaceInstallRequest")


@_attrs_define
class MarketplaceInstallRequest:
    """
    Attributes:
        catalog_id (str):
        pack_id (str):
        version (str):
        url (Union[Unset, str]):
        sha256 (Union[Unset, str]):
        force (Union[Unset, bool]):  Default: False.
        upgrade (Union[Unset, bool]):  Default: False.
        inactive (Union[Unset, bool]):  Default: False.
    """

    catalog_id: str
    pack_id: str
    version: str
    url: Union[Unset, str] = UNSET
    sha256: Union[Unset, str] = UNSET
    force: Union[Unset, bool] = False
    upgrade: Union[Unset, bool] = False
    inactive: Union[Unset, bool] = False
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        catalog_id = self.catalog_id

        pack_id = self.pack_id

        version = self.version

        url = self.url

        sha256 = self.sha256

        force = self.force

        upgrade = self.upgrade

        inactive = self.inactive

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "catalog_id": catalog_id,
                "pack_id": pack_id,
                "version": version,
            }
        )
        if url is not UNSET:
            field_dict["url"] = url
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256
        if force is not UNSET:
            field_dict["force"] = force
        if upgrade is not UNSET:
            field_dict["upgrade"] = upgrade
        if inactive is not UNSET:
            field_dict["inactive"] = inactive

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        catalog_id = d.pop("catalog_id")

        pack_id = d.pop("pack_id")

        version = d.pop("version")

        url = d.pop("url", UNSET)

        sha256 = d.pop("sha256", UNSET)

        force = d.pop("force", UNSET)

        upgrade = d.pop("upgrade", UNSET)

        inactive = d.pop("inactive", UNSET)

        marketplace_install_request = cls(
            catalog_id=catalog_id,
            pack_id=pack_id,
            version=version,
            url=url,
            sha256=sha256,
            force=force,
            upgrade=upgrade,
            inactive=inactive,
        )

        marketplace_install_request.additional_properties = d
        return marketplace_install_request

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
