from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.create_worker_credential_request_proof_algorithm import (
    CreateWorkerCredentialRequestProofAlgorithm,
)
from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="CreateWorkerCredentialRequest")


@_attrs_define
class CreateWorkerCredentialRequest:
    """Creates or rotates a worker credential. Proof-key fields are optional for legacy credentials, but when any proof-key
    field is supplied all three and agent_id must be supplied together. Rotating an active proof credential without
    proof-key fields preserves its enrolled key and authoritative agent link after revalidation; changing agent_id for a
    proof-bearing credential requires a full proof-key replacement bundle.

        Attributes:
            worker_id (str):
            allowed_pools (Union[Unset, List[str]]):
            allowed_topics (Union[Unset, List[str]]):
            agent_id (Union[Unset, str]): Active agent identity in the same tenant; required for proof-key enrollment.
            proof_key_id (Union[Unset, str]):
            proof_algorithm (Union[Unset, CreateWorkerCredentialRequestProofAlgorithm]):
            proof_public_key_pem (Union[Unset, str]): ECDSA P-256 public key encoded as SubjectPublicKeyInfo PEM.
    """

    worker_id: str
    allowed_pools: Union[Unset, List[str]] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    agent_id: Union[Unset, str] = UNSET
    proof_key_id: Union[Unset, str] = UNSET
    proof_algorithm: Union[Unset, CreateWorkerCredentialRequestProofAlgorithm] = UNSET
    proof_public_key_pem: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        worker_id = self.worker_id

        allowed_pools: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_pools, Unset):
            allowed_pools = self.allowed_pools

        allowed_topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_topics, Unset):
            allowed_topics = self.allowed_topics

        agent_id = self.agent_id

        proof_key_id = self.proof_key_id

        proof_algorithm: Union[Unset, str] = UNSET
        if not isinstance(self.proof_algorithm, Unset):
            proof_algorithm = self.proof_algorithm.value

        proof_public_key_pem = self.proof_public_key_pem

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "worker_id": worker_id,
            }
        )
        if allowed_pools is not UNSET:
            field_dict["allowed_pools"] = allowed_pools
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if proof_key_id is not UNSET:
            field_dict["proof_key_id"] = proof_key_id
        if proof_algorithm is not UNSET:
            field_dict["proof_algorithm"] = proof_algorithm
        if proof_public_key_pem is not UNSET:
            field_dict["proof_public_key_pem"] = proof_public_key_pem

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        worker_id = d.pop("worker_id")

        allowed_pools = cast(List[str], d.pop("allowed_pools", UNSET))

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))

        agent_id = d.pop("agent_id", UNSET)

        proof_key_id = d.pop("proof_key_id", UNSET)

        _proof_algorithm = d.pop("proof_algorithm", UNSET)
        proof_algorithm: Union[Unset, CreateWorkerCredentialRequestProofAlgorithm]
        if isinstance(_proof_algorithm, Unset):
            proof_algorithm = UNSET
        else:
            proof_algorithm = CreateWorkerCredentialRequestProofAlgorithm(_proof_algorithm)

        proof_public_key_pem = d.pop("proof_public_key_pem", UNSET)

        create_worker_credential_request = cls(
            worker_id=worker_id,
            allowed_pools=allowed_pools,
            allowed_topics=allowed_topics,
            agent_id=agent_id,
            proof_key_id=proof_key_id,
            proof_algorithm=proof_algorithm,
            proof_public_key_pem=proof_public_key_pem,
        )

        create_worker_credential_request.additional_properties = d
        return create_worker_credential_request

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
