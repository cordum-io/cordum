from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.create_eval_dataset_body_entries_item_metadata import (
        CreateEvalDatasetBodyEntriesItemMetadata,
    )
    from ..models.create_eval_dataset_body_entries_item_input import (
        CreateEvalDatasetBodyEntriesItemInput,
    )


T = TypeVar("T", bound="CreateEvalDatasetBodyEntriesItem")


@_attrs_define
class CreateEvalDatasetBodyEntriesItem:
    """
    Attributes:
        id (Union[Unset, str]):
        input_ (Union[Unset, CreateEvalDatasetBodyEntriesItemInput]):
        expected_decision (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        metadata (Union[Unset, CreateEvalDatasetBodyEntriesItemMetadata]):
        source (Union[Unset, str]):
        source_ref (Union[Unset, str]):
        notes (Union[Unset, str]):
    """

    id: Union[Unset, str] = UNSET
    input_: Union[Unset, "CreateEvalDatasetBodyEntriesItemInput"] = UNSET
    expected_decision: Union[Unset, str] = UNSET
    rule_id: Union[Unset, str] = UNSET
    metadata: Union[Unset, "CreateEvalDatasetBodyEntriesItemMetadata"] = UNSET
    source: Union[Unset, str] = UNSET
    source_ref: Union[Unset, str] = UNSET
    notes: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.create_eval_dataset_body_entries_item_metadata import (
            CreateEvalDatasetBodyEntriesItemMetadata,
        )
        from ..models.create_eval_dataset_body_entries_item_input import (
            CreateEvalDatasetBodyEntriesItemInput,
        )

        id = self.id

        input_: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.input_, Unset):
            input_ = self.input_.to_dict()

        expected_decision = self.expected_decision

        rule_id = self.rule_id

        metadata: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        source = self.source

        source_ref = self.source_ref

        notes = self.notes

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if input_ is not UNSET:
            field_dict["input"] = input_
        if expected_decision is not UNSET:
            field_dict["expected_decision"] = expected_decision
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if source is not UNSET:
            field_dict["source"] = source
        if source_ref is not UNSET:
            field_dict["source_ref"] = source_ref
        if notes is not UNSET:
            field_dict["notes"] = notes

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.create_eval_dataset_body_entries_item_metadata import (
            CreateEvalDatasetBodyEntriesItemMetadata,
        )
        from ..models.create_eval_dataset_body_entries_item_input import (
            CreateEvalDatasetBodyEntriesItemInput,
        )

        d = src_dict.copy()
        id = d.pop("id", UNSET)

        _input_ = d.pop("input", UNSET)
        input_: Union[Unset, CreateEvalDatasetBodyEntriesItemInput]
        if isinstance(_input_, Unset):
            input_ = UNSET
        else:
            input_ = CreateEvalDatasetBodyEntriesItemInput.from_dict(_input_)

        expected_decision = d.pop("expected_decision", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, CreateEvalDatasetBodyEntriesItemMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = CreateEvalDatasetBodyEntriesItemMetadata.from_dict(_metadata)

        source = d.pop("source", UNSET)

        source_ref = d.pop("source_ref", UNSET)

        notes = d.pop("notes", UNSET)

        create_eval_dataset_body_entries_item = cls(
            id=id,
            input_=input_,
            expected_decision=expected_decision,
            rule_id=rule_id,
            metadata=metadata,
            source=source,
            source_ref=source_ref,
            notes=notes,
        )

        create_eval_dataset_body_entries_item.additional_properties = d
        return create_eval_dataset_body_entries_item

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
