from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.shadow_remediation_action_kind import ShadowRemediationActionKind
from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.shadow_remediation_api_request import ShadowRemediationAPIRequest


T = TypeVar("T", bound="ShadowRemediationStep")


@_attrs_define
class ShadowRemediationStep:
    """
    Attributes:
        id (str): Stable, deterministic identifier within the plan.
        title (str):
        kind (ShadowRemediationActionKind):
        command (Union[Unset, str]): Shell-runnable suggestion with literal placeholders. Empty when the step is API-
            only or when omit_commands=true.
        api_request (Union[Unset, ShadowRemediationAPIRequest]):
        requires_backup (Union[Unset, bool]):
        preview_only (Union[Unset, bool]):
        destructive (Union[Unset, bool]):
        docs_url (Union[Unset, str]):
        conditions (Union[Unset, List[str]]):
    """

    id: str
    title: str
    kind: ShadowRemediationActionKind
    command: Union[Unset, str] = UNSET
    api_request: Union[Unset, "ShadowRemediationAPIRequest"] = UNSET
    requires_backup: Union[Unset, bool] = UNSET
    preview_only: Union[Unset, bool] = UNSET
    destructive: Union[Unset, bool] = UNSET
    docs_url: Union[Unset, str] = UNSET
    conditions: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.shadow_remediation_api_request import ShadowRemediationAPIRequest

        id = self.id

        title = self.title

        kind = self.kind.value

        command = self.command

        api_request: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.api_request, Unset):
            api_request = self.api_request.to_dict()

        requires_backup = self.requires_backup

        preview_only = self.preview_only

        destructive = self.destructive

        docs_url = self.docs_url

        conditions: Union[Unset, List[str]] = UNSET
        if not isinstance(self.conditions, Unset):
            conditions = self.conditions

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "title": title,
                "kind": kind,
            }
        )
        if command is not UNSET:
            field_dict["command"] = command
        if api_request is not UNSET:
            field_dict["api_request"] = api_request
        if requires_backup is not UNSET:
            field_dict["requires_backup"] = requires_backup
        if preview_only is not UNSET:
            field_dict["preview_only"] = preview_only
        if destructive is not UNSET:
            field_dict["destructive"] = destructive
        if docs_url is not UNSET:
            field_dict["docs_url"] = docs_url
        if conditions is not UNSET:
            field_dict["conditions"] = conditions

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.shadow_remediation_api_request import ShadowRemediationAPIRequest

        d = src_dict.copy()
        id = d.pop("id")

        title = d.pop("title")

        kind = ShadowRemediationActionKind(d.pop("kind"))

        command = d.pop("command", UNSET)

        _api_request = d.pop("api_request", UNSET)
        api_request: Union[Unset, ShadowRemediationAPIRequest]
        if isinstance(_api_request, Unset):
            api_request = UNSET
        else:
            api_request = ShadowRemediationAPIRequest.from_dict(_api_request)

        requires_backup = d.pop("requires_backup", UNSET)

        preview_only = d.pop("preview_only", UNSET)

        destructive = d.pop("destructive", UNSET)

        docs_url = d.pop("docs_url", UNSET)

        conditions = cast(List[str], d.pop("conditions", UNSET))

        shadow_remediation_step = cls(
            id=id,
            title=title,
            kind=kind,
            command=command,
            api_request=api_request,
            requires_backup=requires_backup,
            preview_only=preview_only,
            destructive=destructive,
            docs_url=docs_url,
            conditions=conditions,
        )

        shadow_remediation_step.additional_properties = d
        return shadow_remediation_step

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
