"""Validate and print the reproducible browser-baseline protocol.

The long Windows measurement is intentionally operator-controlled; this
stdlib-only command prevents fixture drift before a run and prints the exact
timing/metric contract that the raw capture must satisfy.
"""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
FIXTURE = Path(__file__).with_name("fixture.json")
REQUIRED_METRICS = {
    "startup_ms",
    "bridge_to_store_ms",
    "order_intent_to_result_ms",
    "process_tree_cpu_percent",
    "process_tree_private_memory_mb",
    "frame_interval_ms",
    "queue_high_water",
    "coalesces",
    "overflows",
    "disconnects",
    "drops",
    "open_close_recovery_ms",
}
SENSITIVE_KEYS = re.compile(
    r"(?:secret|password|credential|api[_-]?key|account(?:[_-]?id)?|access[_-]?token|private[_-]?key)",
    re.IGNORECASE,
)


def validate_fixture(path: Path) -> dict:
    data = json.loads(path.read_text(encoding="utf-8"))
    if data.get("fixture_id") != "browser-host-baseline-v1":
        raise ValueError("unexpected fixture_id")
    if data.get("platform") != "windows-11-x64":
        raise ValueError("baseline is fixed to windows-11-x64")
    demo = data.get("demo", {})
    symbols = demo.get("symbols", [])
    if len(symbols) != 12 or len(set(symbols)) != 12 or not all(s.startswith("US.") for s in symbols):
        raise ValueError("demo.symbols must contain twelve unique US symbols")
    if demo.get("orders") != "simulated-only" or data.get("order_intent", {}).get("execution") != "simulated-only":
        raise ValueError("order intent must be simulated-only")
    panel_mix = data.get("panel_mix", [])
    if len(panel_mix) != 12 or len({p.get("id") for p in panel_mix}) != 12:
        raise ValueError("panel_mix must contain twelve uniquely identified panels")
    workspaces = data.get("workspaces", [])
    if len(workspaces) != 4 or any(w.get("panels") != "panel_mix" for w in workspaces):
        raise ValueError("fixture must define four Workspaces using panel_mix")
    protocol = data.get("protocol", {})
    if (
        protocol.get("warmup_seconds") != 300
        or protocol.get("measurement_runs") != 3
        or protocol.get("measurement_seconds") != 900
        or protocol.get("sample_interval_seconds") != 1
        or protocol.get("open_close_recovery_cycles") != 10
    ):
        raise ValueError("protocol must be 5 minutes plus three 15-minute runs")
    if set(data.get("metrics", [])) != REQUIRED_METRICS:
        raise ValueError("metric contract drifted")

    for key in iter_nested_keys(data):
        if SENSITIVE_KEYS.search(key):
            raise ValueError(f"sensitive fixture key: {key}")
    return data


def iter_nested_keys(value):
    if isinstance(value, dict):
        for key, child in value.items():
            yield key
            yield from iter_nested_keys(child)
    elif isinstance(value, list):
        for child in value:
            yield from iter_nested_keys(child)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixture", type=Path, default=FIXTURE)
    parser.add_argument("--dry-run", action="store_true", help="validate and print the protocol without waiting")
    args = parser.parse_args()
    data = validate_fixture(args.fixture)
    protocol = data["protocol"]
    result = {
        "fixture": data["fixture_id"],
        "fixture_path": str(args.fixture.relative_to(ROOT)),
        "warmup_seconds": protocol["warmup_seconds"],
        "measurement_runs": protocol["measurement_runs"],
        "measurement_seconds": protocol["measurement_seconds"],
        "sample_interval_seconds": protocol["sample_interval_seconds"],
        "metrics": data["metrics"],
        "workspace_count": len(data["workspaces"]),
        "panels_per_workspace": len(data["panel_mix"]),
        "orders": data["order_intent"]["execution"],
    }
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
