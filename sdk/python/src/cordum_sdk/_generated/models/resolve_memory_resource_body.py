from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import Dict

if TYPE_CHECKING:
  from ..models.resolve_memory_resource_body_reference import ResolveMemoryResourceBodyReference





T = TypeVar("T", bound="ResolveMemoryResourceBody")


@_attrs_define
class ResolveMemoryResourceBody:
    """ 
        Attributes:
            job_id (str):
            reference (ResolveMemoryResourceBodyReference):
     """

    job_id: str
    reference: 'ResolveMemoryResourceBodyReference'


    def to_dict(self) -> Dict[str, Any]:
        from ..models.resolve_memory_resource_body_reference import ResolveMemoryResourceBodyReference
        job_id = self.job_id

        reference = self.reference.to_dict()


        field_dict: Dict[str, Any] = {}
        field_dict.update({
            "job_id": job_id,
            "reference": reference,
        })

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.resolve_memory_resource_body_reference import ResolveMemoryResourceBodyReference
        d = src_dict.copy()
        job_id = d.pop("job_id")

        reference = ResolveMemoryResourceBodyReference.from_dict(d.pop("reference"))




        resolve_memory_resource_body = cls(
            job_id=job_id,
            reference=reference,
        )

        return resolve_memory_resource_body

