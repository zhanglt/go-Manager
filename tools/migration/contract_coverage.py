#!/usr/bin/env python3
"""Measure contract manifest coverage against the generated Scala route inventory."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit


def build_report(inventory: dict[str, Any], manifests: list[tuple[str, dict[str, Any]]]) -> dict[str, Any]:
    actions = inventory["actions"]
    by_id = {action["route_id"]: action for action in actions}
    by_request: dict[tuple[str, str], list[str]] = {}
    for action in actions:
        by_request.setdefault((action["method"], action["path"]), []).append(action["route_id"])

    covered: set[str] = set()
    unknown_cases: list[dict[str, Any]] = []
    ambiguous_cases: list[dict[str, Any]] = []
    duplicate_case_ids: list[str] = []
    seen_case_ids: set[str] = set()
    case_count = 0

    for manifest_name, manifest in manifests:
        if manifest.get("schema_version") != 1:
            raise ValueError(f"unsupported manifest schema_version: {manifest_name}")
        for case in manifest.get("cases", []):
            case_count += 1
            case_id = case["id"]
            if case_id in seen_case_ids:
                duplicate_case_ids.append(case_id)
            seen_case_ids.add(case_id)
            explicit = case.get("covers")
            if explicit is not None:
                missing_ids = sorted(set(explicit) - set(by_id))
                if missing_ids:
                    unknown_cases.append({"manifest": manifest_name, "case": case_id, "route_ids": missing_ids})
                covered.update(set(explicit) & set(by_id))
                continue

            request = case["request"]
            key = (request.get("method", "GET").upper(), urlsplit(request["path"]).path)
            matches = by_request.get(key, [])
            if not matches:
                matches = by_request.get(("ANY", key[1]), [])
            if len(matches) == 1:
                covered.add(matches[0])
            elif not matches:
                unknown_cases.append(
                    {"manifest": manifest_name, "case": case_id, "method": key[0], "path": key[1]}
                )
            else:
                ambiguous_cases.append(
                    {"manifest": manifest_name, "case": case_id, "method": key[0], "path": key[1], "route_ids": matches}
                )

    missing = [
        {
            "route_id": action["route_id"],
            "domain": action["domain"],
            "method": action["method"],
            "path": action["path"],
            "source": action["source"],
            "line": action["line"],
        }
        for action in actions
        if action["route_id"] not in covered
    ]
    return {
        "schema_version": 1,
        "route_count": len(actions),
        "case_count": case_count,
        "covered_route_count": len(covered),
        "coverage_percent": round(len(covered) * 100 / len(actions), 2) if actions else 100,
        "missing_route_count": len(missing),
        "ambiguous_case_count": len(ambiguous_cases),
        "unknown_case_count": len(unknown_cases),
        "duplicate_case_ids": sorted(set(duplicate_case_ids)),
        "ambiguous_cases": ambiguous_cases,
        "unknown_cases": unknown_cases,
        "missing_routes": missing,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--routes", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, action="append", required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--strict", action="store_true", help="Fail unless coverage is complete and unambiguous")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    inventory = json.loads(args.routes.read_text(encoding="utf-8"))
    manifests = [(str(path), json.loads(path.read_text(encoding="utf-8"))) for path in args.manifest]
    report = build_report(inventory, manifests)
    rendered = json.dumps(report, indent=2) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered, encoding="utf-8")
    else:
        print(rendered, end="")
    incomplete = (
        report["missing_route_count"]
        or report["ambiguous_case_count"]
        or report["unknown_case_count"]
        or report["duplicate_case_ids"]
    )
    return 1 if args.strict and incomplete else 0


if __name__ == "__main__":
    raise SystemExit(main())
