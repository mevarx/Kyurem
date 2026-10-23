#!/usr/bin/env python3
import json
import sys
from collections import Counter
from pathlib import Path


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: check-a2a-tck-report.py <compatibility.json>", file=sys.stderr)
        return 2

    report_path = Path(sys.argv[1])
    try:
        report = json.loads(report_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"cannot read A2A TCK compatibility report {report_path}: {exc}", file=sys.stderr)
        return 1

    requirements = report.get("per_requirement") if isinstance(report, dict) else None
    if not isinstance(requirements, dict):
        print(f"invalid A2A TCK compatibility report {report_path}: missing per_requirement", file=sys.stderr)
        return 1

    must = {
        requirement_id: result
        for requirement_id, result in requirements.items()
        if isinstance(result, dict) and str(result.get("level", "")).upper() == "MUST"
    }
    if not must:
        print(f"invalid A2A TCK compatibility report {report_path}: no MUST requirements", file=sys.stderr)
        return 1

    statuses = Counter(str(result.get("status", "UNKNOWN")).upper() for result in must.values())
    failures = {
        requirement_id: result
        for requirement_id, result in must.items()
        if str(result.get("status", "")).upper() == "FAIL"
    }
    valid_statuses = {"PASS", "FAIL", "SKIPPED", "NOT TESTED"}
    unknown = {
        requirement_id: result
        for requirement_id, result in must.items()
        if str(result.get("status", "UNKNOWN")).upper() not in valid_statuses
    }
    summary = report.get("summary", {})
    compatibility = summary.get("must_compatibility") if isinstance(summary, dict) else None
    compatibility_text = f"; MUST compatibility: {compatibility}" if compatibility else ""
    print(
        "A2A TCK MUST results: "
        f"PASS={statuses['PASS']} FAIL={statuses['FAIL']} "
        f"SKIPPED={statuses['SKIPPED']} NOT TESTED={statuses['NOT TESTED']}"
        f"{compatibility_text}"
    )
    if statuses["NOT TESTED"]:
        print("NOT TESTED requirements are outside this profile's verified coverage.", file=sys.stderr)
    for requirement_id, result in failures.items():
        errors = result.get("errors") or []
        detail = "; ".join(str(error) for error in errors) if isinstance(errors, list) else str(errors)
        print(f"FAIL {requirement_id}" + (f": {detail}" if detail else ""), file=sys.stderr)
    for requirement_id in unknown:
        print(f"invalid status for MUST requirement {requirement_id}", file=sys.stderr)

    return 1 if failures or unknown else 0


if __name__ == "__main__":
    raise SystemExit(main())
