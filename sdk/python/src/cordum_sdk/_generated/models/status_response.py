from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
  from ..models.status_response_nats import StatusResponseNats
  from ..models.status_response_redis import StatusResponseRedis
  from ..models.status_response_build import StatusResponseBuild
  from ..models.status_response_license_type_0 import StatusResponseLicenseType0





T = TypeVar("T", bound="StatusResponse")


@_attrs_define
class StatusResponse:
    """ 
        Attributes:
            time (Union[Unset, datetime.datetime]):
            uptime_seconds (Union[Unset, float]):
            build (Union[Unset, StatusResponseBuild]):
            nats (Union[Unset, StatusResponseNats]):
            redis (Union[Unset, StatusResponseRedis]):
            workers (Union[Unset, int]): Number of connected workers
            license_ (Union['StatusResponseLicenseType0', None, Unset]):
     """

    time: Union[Unset, datetime.datetime] = UNSET
    uptime_seconds: Union[Unset, float] = UNSET
    build: Union[Unset, 'StatusResponseBuild'] = UNSET
    nats: Union[Unset, 'StatusResponseNats'] = UNSET
    redis: Union[Unset, 'StatusResponseRedis'] = UNSET
    workers: Union[Unset, int] = UNSET
    license_: Union['StatusResponseLicenseType0', None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.status_response_nats import StatusResponseNats
        from ..models.status_response_redis import StatusResponseRedis
        from ..models.status_response_build import StatusResponseBuild
        from ..models.status_response_license_type_0 import StatusResponseLicenseType0
        time: Union[Unset, str] = UNSET
        if not isinstance(self.time, Unset):
            time = self.time.isoformat()

        uptime_seconds = self.uptime_seconds

        build: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.build, Unset):
            build = self.build.to_dict()

        nats: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.nats, Unset):
            nats = self.nats.to_dict()

        redis: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.redis, Unset):
            redis = self.redis.to_dict()

        workers = self.workers

        license_: Union[Dict[str, Any], None, Unset]
        if isinstance(self.license_, Unset):
            license_ = UNSET
        elif isinstance(self.license_, StatusResponseLicenseType0):
            license_ = self.license_.to_dict()
        else:
            license_ = self.license_


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if time is not UNSET:
            field_dict["time"] = time
        if uptime_seconds is not UNSET:
            field_dict["uptime_seconds"] = uptime_seconds
        if build is not UNSET:
            field_dict["build"] = build
        if nats is not UNSET:
            field_dict["nats"] = nats
        if redis is not UNSET:
            field_dict["redis"] = redis
        if workers is not UNSET:
            field_dict["workers"] = workers
        if license_ is not UNSET:
            field_dict["license"] = license_

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.status_response_nats import StatusResponseNats
        from ..models.status_response_redis import StatusResponseRedis
        from ..models.status_response_build import StatusResponseBuild
        from ..models.status_response_license_type_0 import StatusResponseLicenseType0
        d = src_dict.copy()
        _time = d.pop("time", UNSET)
        time: Union[Unset, datetime.datetime]
        if isinstance(_time,  Unset):
            time = UNSET
        else:
            time = isoparse(_time)




        uptime_seconds = d.pop("uptime_seconds", UNSET)

        _build = d.pop("build", UNSET)
        build: Union[Unset, StatusResponseBuild]
        if isinstance(_build,  Unset):
            build = UNSET
        else:
            build = StatusResponseBuild.from_dict(_build)




        _nats = d.pop("nats", UNSET)
        nats: Union[Unset, StatusResponseNats]
        if isinstance(_nats,  Unset):
            nats = UNSET
        else:
            nats = StatusResponseNats.from_dict(_nats)




        _redis = d.pop("redis", UNSET)
        redis: Union[Unset, StatusResponseRedis]
        if isinstance(_redis,  Unset):
            redis = UNSET
        else:
            redis = StatusResponseRedis.from_dict(_redis)




        workers = d.pop("workers", UNSET)

        def _parse_license_(data: object) -> Union['StatusResponseLicenseType0', None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                license_type_0 = StatusResponseLicenseType0.from_dict(data)



                return license_type_0
            except: # noqa: E722
                pass
            return cast(Union['StatusResponseLicenseType0', None, Unset], data)

        license_ = _parse_license_(d.pop("license", UNSET))


        status_response = cls(
            time=time,
            uptime_seconds=uptime_seconds,
            build=build,
            nats=nats,
            redis=redis,
            workers=workers,
            license_=license_,
        )


        status_response.additional_properties = d
        return status_response

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
