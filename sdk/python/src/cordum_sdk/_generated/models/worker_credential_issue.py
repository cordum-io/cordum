from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.worker_credential_proof_algorithm import WorkerCredentialProofAlgorithm
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="WorkerCredentialIssue")


@_attrs_define
class WorkerCredentialIssue:
    """
    Attributes:
        worker_id (str):
        tenant_id (str):
        created_by (str):
        created_at (datetime.datetime):
        token (str):
        allowed_pools (Union[Unset, List[str]]):
        allowed_topics (Union[Unset, List[str]]):
        pack_id (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        proof_key_id (Union[Unset, str]):
        proof_algorithm (Union[Unset, WorkerCredentialProofAlgorithm]):
        revoked_at (Union[Unset, datetime.datetime]):
    """

    worker_id: str
    tenant_id: str
    created_by: str
    created_at: datetime.datetime
    token: str
    allowed_pools: Union[Unset, List[str]] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    pack_id: Union[Unset, str] = UNSET
    agent_id: Union[Unset, str] = UNSET
    proof_key_id: Union[Unset, str] = UNSET
    proof_algorithm: Union[Unset, WorkerCredentialProofAlgorithm] = UNSET
    revoked_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        worker_id = self.worker_id

        tenant_id = self.tenant_id

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        token = self.token

        allowed_pools: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_pools, Unset):
            allowed_pools = self.allowed_pools

        allowed_topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_topics, Unset):
            allowed_topics = self.allowed_topics

        pack_id = self.pack_id

        agent_id = self.agent_id

        proof_key_id = self.proof_key_id

        proof_algorithm: Union[Unset, str] = UNSET
        if not isinstance(self.proof_algorithm, Unset):
            proof_algorithm = self.proof_algorithm.value

        revoked_at: Union[Unset, str] = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "worker_id": worker_id,
                "tenant_id": tenant_id,
                "created_by": created_by,
                "created_at": created_at,
                "token": token,
            }
        )
        if allowed_pools is not UNSET:
            field_dict["allowed_pools"] = allowed_pools
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if pack_id is not UNSET:
            field_dict["pack_id"] = pack_id
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if proof_key_id is not UNSET:
            field_dict["proof_key_id"] = proof_key_id
        if proof_algorithm is not UNSET:
            field_dict["proof_algorithm"] = proof_algorithm
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        worker_id = d.pop("worker_id")

        tenant_id = d.pop("tenant_id")

        created_by = d.pop("created_by")

        created_at = isoparse(d.pop("created_at"))

        token = d.pop("token")

        allowed_pools = cast(List[str], d.pop("allowed_pools", UNSET))

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))

        pack_id = d.pop("pack_id", UNSET)

        agent_id = d.pop("agent_id", UNSET)

        proof_key_id = d.pop("proof_key_id", UNSET)

        _proof_algorithm = d.pop("proof_algorithm", UNSET)
        proof_algorithm: Union[Unset, WorkerCredentialProofAlgorithm]
        if isinstance(_proof_algorithm, Unset):
            proof_algorithm = UNSET
        else:
            proof_algorithm = WorkerCredentialProofAlgorithm(_proof_algorithm)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: Union[Unset, datetime.datetime]
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = isoparse(_revoked_at)

        worker_credential_issue = cls(
            worker_id=worker_id,
            tenant_id=tenant_id,
            created_by=created_by,
            created_at=created_at,
            token=token,
            allowed_pools=allowed_pools,
            allowed_topics=allowed_topics,
            pack_id=pack_id,
            agent_id=agent_id,
            proof_key_id=proof_key_id,
            proof_algorithm=proof_algorithm,
            revoked_at=revoked_at,
        )

        worker_credential_issue.additional_properties = d
        return worker_credential_issue

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
