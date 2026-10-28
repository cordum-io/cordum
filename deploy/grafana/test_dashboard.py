"""Regression checks for the shipped dashboard's observability contract.

Run with: python -m unittest discover -s deploy/grafana -p 'test_*.py' -v
Live Prometheus queries and Grafana import are validated separately by the demo.
"""

import json
from pathlib import Path
import re
import unittest


class DashboardContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dashboard = json.loads(
            Path(__file__).with_name("cordum-overview.json").read_text(encoding="utf-8")
        )
        cls.panels = cls.dashboard["panels"]
        cls.targets = [
            (panel, target)
            for panel in cls.panels
            for target in panel.get("targets", [])
        ]

    def test_dispatches_are_not_presented_as_safety_allow_decisions(self):
        for panel, target in self.targets:
            if "cordum_scheduler_jobs_dispatched_total" in target["expr"]:
                self.assertNotIn("allow", target.get("legendFormat", "").lower())
                self.assertNotEqual(panel["title"], "Safety Decisions")

    def test_unwired_workflow_counters_are_not_queried(self):
        expressions = "\n".join(target["expr"] for _, target in self.targets)
        self.assertNotIn("cordum_workflows_started_total", expressions)
        self.assertNotIn("cordum_workflows_completed_total", expressions)

    def test_approval_queue_resolutions_and_latency_are_observable(self):
        expressions = "\n".join(target["expr"] for _, target in self.targets)
        for metric in (
            "cordum_approval_queue_depth",
            "cordum_approval_decisions_total",
            "cordum_approval_latency_seconds_bucket",
        ):
            self.assertIn(metric, expressions)

    def test_approval_queue_snapshots_are_not_summed_across_reporters(self):
        targets = [
            target
            for _, target in self.targets
            if "cordum_approval_queue_depth" in target["expr"]
        ]
        self.assertTrue(targets, "pending approval visibility is required")
        for target in targets:
            self.assertNotRegex(target["expr"], r"\bsum\s*\(")
            self.assertIn("{{pod}}", target["legendFormat"])

    def test_namespace_all_includes_metrics_without_kubernetes_labels(self):
        namespace = next(
            variable
            for variable in self.dashboard["templating"]["list"]
            if variable["name"] == "namespace"
        )
        self.assertTrue(namespace["includeAll"])
        self.assertEqual(namespace.get("allValue"), ".*")

    def test_visible_selectors_filter_at_least_one_panel(self):
        expressions = "\n".join(target["expr"] for _, target in self.targets)
        for variable in self.dashboard["templating"]["list"]:
            if variable.get("hide", 0) == 0:
                self.assertRegex(
                    expressions,
                    r"\$\{?" + re.escape(variable["name"]) + r"\b",
                    f"{variable['name']} selector has no effect on panels",
                )


if __name__ == "__main__":
    unittest.main()
