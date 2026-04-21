"""JUnit XML emitter for the Python harness.

Tight mirror of the Go harness's report shape — downstream CI tooling
(step 8's aggregator) consumes both streams under the same schema.
"""

from __future__ import annotations

import os
import xml.etree.ElementTree as ET
from dataclasses import dataclass, field
from typing import Optional


@dataclass
class TestCase:
    name: str
    class_name: str
    time_sec: float
    failure_message: Optional[str] = None
    failure_body: str = ""


@dataclass
class TestSuite:
    name: str
    cases: list[TestCase] = field(default_factory=list)

    @property
    def tests(self) -> int:
        return len(self.cases)

    @property
    def failures(self) -> int:
        return sum(1 for c in self.cases if c.failure_message)

    def to_xml(self) -> bytes:
        root = ET.Element(
            "testsuite",
            attrib={
                "name": self.name,
                "tests": str(self.tests),
                "failures": str(self.failures),
            },
        )
        for c in self.cases:
            tc = ET.SubElement(
                root,
                "testcase",
                attrib={
                    "name": c.name,
                    "classname": c.class_name,
                    "time": f"{c.time_sec:.7f}",
                },
            )
            if c.failure_message:
                fail = ET.SubElement(
                    tc,
                    "failure",
                    attrib={"message": c.failure_message, "type": "AssertionError"},
                )
                fail.text = c.failure_body
        ET.indent(root, space="  ")
        return b'<?xml version="1.0" encoding="UTF-8"?>\n' + ET.tostring(root, encoding="utf-8")


def write_report(path: str, suite: TestSuite) -> None:
    os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
    with open(path, "wb") as f:
        f.write(suite.to_xml())
