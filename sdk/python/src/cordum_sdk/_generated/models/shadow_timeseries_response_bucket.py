from enum import Enum


class ShadowTimeseriesResponseBucket(str, Enum):
    VALUE_0 = "1m"
    VALUE_1 = "5m"
    VALUE_2 = "15m"
    VALUE_3 = "1h"
    VALUE_4 = "1d"

    def __str__(self) -> str:
        return str(self.value)
