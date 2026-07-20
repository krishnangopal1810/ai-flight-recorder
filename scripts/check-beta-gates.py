#!/usr/bin/env python3
"""Block beta/stable releases until the PRD's externally measured gates pass."""

from __future__ import annotations

import json
from pathlib import Path


document = json.loads(Path("docs/beta-gates.json").read_text())
checks = {
    "supported_incident_reproduction_rate": lambda value: value >= 0.80,
    "diagnosis_time_improvement": lambda value: value >= 0.50,
    "first_divergence_precision": lambda value: value >= 0.80,
    "initial_integration_minutes": lambda value: value < 30,
    "unintended_replay_side_effects": lambda value: value == 0,
    "p95_recording_overhead": lambda value: value < 0.05,
}
failures: list[str] = []
if not document.get("measured_at") or not document.get("reviewer"):
    failures.append("measured_at and reviewer must identify the approved evidence set")
for name, passes in checks.items():
    metric = document.get("metrics", {}).get(name, {})
    value = metric.get("value")
    if metric.get("status") != "passed" or not isinstance(value, (int, float)) or not passes(value):
        failures.append(f"{name}: gate is not satisfied")
    if not metric.get("evidence"):
        failures.append(f"{name}: evidence links are required")
if failures:
    raise SystemExit("beta-gate: blocked\n" + "\n".join(f"- {failure}" for failure in failures))
print("beta-gate: all PRD metrics passed")
