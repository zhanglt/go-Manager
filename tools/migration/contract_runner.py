#!/usr/bin/env python3
"""Run the same HTTP contract cases against Scala and Go Manager endpoints."""

from __future__ import annotations

import argparse
import json
from datetime import datetime, timezone
from pathlib import Path

from contract_lib import compare, send_request


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--left-url", required=True, help="Baseline Manager URL, normally Scala")
    parser.add_argument("--right-url", required=True, help="Candidate Manager URL, normally Go")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=120)
    parser.add_argument("--insecure", action="store_true", help="Accept test Manager certificates")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    manifest = json.loads(args.manifest.read_text(encoding="utf-8"))
    if manifest.get("schema_version") != 1:
        raise SystemExit("unsupported or missing manifest schema_version")
    normalization = manifest.get("normalization", {})
    global_headers = set(normalization.get("ignored_headers", []))
    global_pointers = list(normalization.get("ignored_json_pointers", []))

    results = []
    failed = 0
    for case in manifest.get("cases", []):
        request = case["request"]
        left = send_request(args.left_url, request, args.timeout, args.insecure)
        right = send_request(args.right_url, request, args.timeout, args.insecure)
        case_normalization = case.get("normalization", {})
        ignored_headers = global_headers | set(case_normalization.get("ignored_headers", []))
        ignored_pointers = global_pointers + list(case_normalization.get("ignored_json_pointers", []))
        differences = compare(left, right, ignored_headers, ignored_pointers)
        if differences:
            failed += 1
        results.append(
            {
                "id": case["id"],
                "passed": not differences,
                "differences": differences,
                "left": left.summary(),
                "right": right.summary(),
            }
        )

    report = {
        "schema_version": 1,
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "manifest": str(args.manifest),
        "left_url": args.left_url,
        "right_url": args.right_url,
        "case_count": len(results),
        "passed": len(results) - failed,
        "failed": failed,
        "results": results,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
