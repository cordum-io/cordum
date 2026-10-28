"""Import the shipped dashboard and record real queries; no synthetic metrics.

Requires running local demo services. Credentials are read only from the process
environment and never written to the evidence file. Python standard library only.
"""

import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
import time
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener


OPENER = build_opener(ProxyHandler({}))
GRAFANA = "http://127.0.0.1:33000"
PROMETHEUS = "http://127.0.0.1:39091"


def request(base, path, payload=None, authenticated=False):
    headers = {"Content-Type": "application/json"}
    if authenticated:
        password = os.environ["GRAFANA_DEMO_PASSWORD"]
        encoded = base64.b64encode(("admin:" + password).encode()).decode()
        headers["Authorization"] = "Basic " + encoded
    data = None if payload is None else json.dumps(payload).encode()
    with OPENER.open(Request(base + path, data=data, headers=headers), timeout=15) as response:
        return json.load(response)


def main():
    dashboard = json.loads(Path(__file__).with_name("cordum-overview.json").read_text())
    imported = request(GRAFANA, "/api/dashboards/import", {
        "dashboard": dashboard,
        "overwrite": True,
        "inputs": [{"name": "DS_PROMETHEUS", "type": "datasource",
                    "pluginId": "prometheus", "value": "cordum-local"}],
    }, authenticated=True)
    stored = request(GRAFANA, "/api/dashboards/uid/cordum-overview")["dashboard"]
    if "${DS_PROMETHEUS}" in json.dumps(stored):
        raise RuntimeError("Grafana import left an unresolved data source")
    health = request(GRAFANA, "/api/datasources/uid/cordum-local/health")
    if health.get("status") != "OK":
        raise RuntimeError("Grafana's Prometheus data source is unhealthy")
    targets = request(PROMETHEUS, "/api/v1/targets")["data"]["activeTargets"]
    if len(targets) != 2 or any(target["health"] != "up" for target in targets):
        raise RuntimeError("Both actual Cordum exporters must be up before validation")
    now = int(time.time())
    evidence = {
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "grafana": request(GRAFANA, "/api/health"),
        "prometheus": request(PROMETHEUS, "/api/v1/status/buildinfo")["data"],
        "dashboard_url": GRAFANA + imported.get("url", "/d/cordum-overview"),
        "targets": [{"job": t["labels"]["job"], "health": t["health"]} for t in targets],
        "queries": [],
    }
    for panel in stored["panels"]:
        for target in panel.get("targets", []):
            expression = target["expr"].replace("$namespace", ".*").replace("$__rate_interval", "1m")
            params = urlencode({"query": expression, "start": now - 300, "end": now, "step": 2})
            result = request(PROMETHEUS, "/api/v1/query_range?" + params)
            if result["status"] != "success":
                raise RuntimeError("Prometheus query failed: " + expression)
            series = result["data"]["result"]
            grafana_result = request(GRAFANA, "/api/ds/query", {
                "from": str((now - 300) * 1000), "to": str(now * 1000),
                "queries": [{"refId": "A", "datasource": {"uid": "cordum-local"},
                             "expr": expression, "range": True, "instant": False,
                             "intervalMs": 2000, "maxDataPoints": 1000}],
            })["results"]["A"]
            if grafana_result.get("error") or grafana_result.get("status", 200) >= 400:
                raise RuntimeError("Grafana query failed: " + str(grafana_result.get("error")))
            evidence["queries"].append({"panel": panel["title"], "expression": expression,
                                        "series_count": len(series), "series": series,
                                        "grafana_frames": len(grafana_result.get("frames", []))})
    output = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("grafana-evidence.json")
    output.write_text(json.dumps(evidence, indent=2), encoding="utf-8")
    empty = [q["panel"] for q in evidence["queries"] if not q["series_count"]]
    print(json.dumps({"dashboard_url": evidence["dashboard_url"], "queries": len(evidence["queries"]),
                      "empty_panels": empty, "evidence": str(output)}, indent=2))


if __name__ == "__main__":
    main()
