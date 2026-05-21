from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import Dict

if TYPE_CHECKING:
    from ..models.schema_record_schema import SchemaRecordSchema


T = TypeVar("T", bound="SchemaRecord")


@_attrs_define
class SchemaRecord:
    """
    Attributes:
        id (str):
        schema (SchemaRecordSchema): JSON Schema document
    """

    id: str
    schema: "SchemaRecordSchema"
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.schema_record_schema import SchemaRecordSchema

        id = self.id

        schema = self.schema.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "schema": schema,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.schema_record_schema import SchemaRecordSchema

        d = src_dict.copy()
        id = d.pop("id")

        schema = SchemaRecordSchema.from_dict(d.pop("schema"))

        schema_record = cls(
            id=id,
            schema=schema,
        )

        schema_record.additional_properties = d
        return schema_record

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
