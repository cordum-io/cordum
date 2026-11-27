from typing import Any, Dict, Type, TypeVar, TYPE_CHECKING


from attrs import define as _attrs_define

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.edge_labels import EdgeLabels


T = TypeVar("T", bound="EdgeCalibrationReportRequest")


@_attrs_define
class EdgeCalibrationReportRequest:
    """
    Attributes:
        since (datetime.datetime): Start of the window, inclusive.
        until (datetime.datetime): End of the window, exclusive. At most 90 days after since.
        repos (Union[Unset, List[str]]): When set, only sessions in these repositories are counted.
        labels (Union[Unset, EdgeLabels]):
    """

    since: datetime.datetime
    until: datetime.datetime
    repos: Union[Unset, List[str]] = UNSET
    labels: Union[Unset, "EdgeLabels"] = UNSET

    def to_dict(self) -> Dict[str, Any]:
        since = self.since.isoformat()

        until = self.until.isoformat()

        repos: Union[Unset, List[str]] = UNSET
        if not isinstance(self.repos, Unset):
            repos = self.repos

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(
            {
                "since": since,
                "until": until,
            }
        )
        if repos is not UNSET:
            field_dict["repos"] = repos
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_labels import EdgeLabels

        d = src_dict.copy()
        since = isoparse(d.pop("since"))

        until = isoparse(d.pop("until"))

        repos = cast(List[str], d.pop("repos", UNSET))

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeLabels.from_dict(_labels)

        edge_calibration_report_request = cls(
            since=since,
            until=until,
            repos=repos,
            labels=labels,
        )

        return edge_calibration_report_request
