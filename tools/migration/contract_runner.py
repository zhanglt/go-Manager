#!/usr/bin/env python3
"""Run the same HTTP contract cases against Scala and Go Manager endpoints."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from datetime import date, datetime, timezone
from pathlib import Path
from typing import Any

from contract_lib import compare, send_request


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--left-url", required=True, help="Baseline Manager URL, normally Scala")
    parser.add_argument("--right-url", required=True, help="Candidate Manager URL, normally Go")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=120)
    parser.add_argument("--insecure", action="store_true", help="Accept test Manager certificates")
    parser.add_argument("--approvals", type=Path, help="Exact, owner-approved expected differences")
    parser.add_argument("--approval-candidates-output", type=Path, help="Write unapproved exact differences for QA review")
    parser.add_argument("--strict-normalization", action="store_true")
    return parser.parse_args()


def validate_normalization(manifest: dict[str, Any]) -> None:
    allowed_global_headers = {"date", "server"}
    normalizations = [("global", manifest.get("normalization", {}))]
    normalizations.extend(
        (str(case.get("id")), case.get("normalization", {})) for case in manifest.get("cases", [])
    )
    for location, normalization in normalizations:
        headers = {str(name).lower() for name in normalization.get("ignored_headers", [])}
        if location == "global" and not headers <= allowed_global_headers:
            raise ValueError(f"global normalization contains functional headers: {sorted(headers)}")
        if location != "global" and headers:
            raise ValueError(f"case {location} may not ignore response headers in strict mode")
        pointers = [str(pointer) for pointer in normalization.get("ignored_json_pointers", [])]
        if any(pointer in {"", "/"} or "*" in pointer for pointer in pointers):
            raise ValueError(f"normalization at {location} contains a broad JSON ignore")
        if (headers or pointers) and not str(normalization.get("reason", "")).strip():
            raise ValueError(f"normalization at {location} is missing a reason")


def load_approvals(path: Path | None) -> tuple[dict[tuple[str, str, str], dict[str, Any]], dict[str, Any]]:
    if path is None:
        return {}, {}
    document = json.loads(path.read_text(encoding="utf-8"))
    if document.get("schema_version") != 1:
        raise ValueError("unsupported or missing approvals schema_version")
    required = ("approved_by", "approval_ticket", "approved_at")
    if any(not str(document.get(name, "")).strip() for name in required):
        raise ValueError("difference approvals require approved_by, approval_ticket and approved_at")
    approvals: dict[tuple[str, str, str], dict[str, Any]] = {}
    for approval in document.get("differences", []):
        key = (str(approval.get("case_id", "")), str(approval.get("kind", "")), str(approval.get("path", "")))
        body_approval = key[1] == "body" and key[2] == "/body"
        broad_path = key[2] in {"/", "/headers"} or (key[2] == "/body" and not body_approval)
        if not all(key) or "*" in "".join(key) or broad_path or key in approvals:
            raise ValueError(f"difference approval must be unique and exact: {key}")
        if body_approval:
            digests = (str(approval.get("left_sha256", "")), str(approval.get("right_sha256", "")))
            if any(not re.fullmatch(r"[0-9a-f]{64}", digest) for digest in digests):
                raise ValueError(f"body difference approval requires exact left/right SHA-256: {key}")
        if not str(approval.get("reason", "")).strip():
            raise ValueError(f"difference approval is missing a reason: {key}")
        expires_at = str(approval.get("expires_at", ""))
        if expires_at and date.fromisoformat(expires_at) < datetime.now(timezone.utc).date():
            raise ValueError(f"difference approval has expired: {key}")
        approvals[key] = approval
    metadata = {name: document[name] for name in required}
    return approvals, metadata


def main() -> int:
    args = parse_args()
    manifest = json.loads(args.manifest.read_text(encoding="utf-8"))
    if manifest.get("schema_version") != 1:
        raise SystemExit("unsupported or missing manifest schema_version")
    if args.strict_normalization:
        validate_normalization(manifest)
    approvals, approval_metadata = load_approvals(args.approvals)
    normalization = manifest.get("normalization", {})
    global_headers = set(normalization.get("ignored_headers", []))
    global_pointers = list(normalization.get("ignored_json_pointers", []))

    results = []
    failed = 0
    exact_matches = 0
    approved_difference_count = 0
    used_approvals: set[tuple[str, str, str]] = set()
    for case in manifest.get("cases", []):
        request = case["request"]
        left = send_request(args.left_url, request, args.timeout, args.insecure)
        right = send_request(args.right_url, request, args.timeout, args.insecure)
        case_normalization = case.get("normalization", {})
        ignored_headers = global_headers | set(case_normalization.get("ignored_headers", []))
        ignored_pointers = global_pointers + list(case_normalization.get("ignored_json_pointers", []))
        differences = compare(left, right, ignored_headers, ignored_pointers)
        approved_differences = []
        unexplained_differences = []
        for difference in differences:
            key = (str(case["id"]), str(difference["kind"]), str(difference["path"]))
            approval = approvals.get(key)
            body_hashes_match = difference["kind"] != "body" or (
                approval is not None
                and approval.get("left_sha256") == left.summary()["decoded_body_sha256"]
                and approval.get("right_sha256") == right.summary()["decoded_body_sha256"]
            )
            if approval is not None and body_hashes_match:
                used_approvals.add(key)
                approved_differences.append({**difference, "approval": approval})
            else:
                unexplained_differences.append(difference)
        if unexplained_differences:
            failed += 1
        if not differences:
            exact_matches += 1
        approved_difference_count += len(approved_differences)
        results.append(
            {
                "id": case["id"],
                "passed": not unexplained_differences,
                "exact_match": not differences,
                "differences": differences,
                "approved_differences": approved_differences,
                "unexplained_differences": unexplained_differences,
                "left": left.summary(),
                "right": right.summary(),
            }
        )

    unknown_approvals = sorted("/".join(key) for key in set(approvals) - used_approvals)
    if unknown_approvals:
        failed += 1
    result_by_id = {result["id"]: result for result in results}
    case_by_id = {str(case["id"]): case for case in manifest.get("cases", [])}
    feature_results = {}
    for feature, requirement in manifest.get("features", {}).items():
        if isinstance(requirement, list):
            requirement = {"cases": requirement}
        ids = [str(case_id) for case_id in requirement.get("cases", [])]
        selected_results = [result_by_id.get(case_id, {}) for case_id in ids]
        required_headers = {str(name).lower() for name in requirement.get("response_headers", [])}
        required_request_headers = {str(name).lower() for name in requirement.get("request_headers", [])}
        minimum_status = int(requirement.get("status_min", 0))
        minimum_body = int(requirement.get("min_decoded_body_bytes", 0))
        assertions_passed = True
        for result in selected_results:
            for side in ("left", "right"):
                summary = result.get(side, {})
                assertions_passed = assertions_passed and summary.get("status") is not None
                assertions_passed = assertions_passed and required_headers <= set(summary.get("headers", {}))
                assertions_passed = assertions_passed and int(summary.get("status") or 0) >= minimum_status
                assertions_passed = assertions_passed and int(summary.get("decoded_body_bytes") or 0) >= minimum_body
        for case_id in ids:
            request_headers = {
                str(name).lower()
                for name in case_by_id.get(case_id, {}).get("request", {}).get("headers", {})
            }
            assertions_passed = assertions_passed and required_request_headers <= request_headers
        feature_results[str(feature)] = {
            "case_ids": ids,
            "passed": bool(ids) and all(result.get("passed") for result in selected_results) and assertions_passed,
            "requirements": {
                "response_headers": sorted(required_headers),
                "request_headers": sorted(required_request_headers),
                "status_min": minimum_status,
                "min_decoded_body_bytes": minimum_body,
            },
        }

    report = {
        "schema_version": 1,
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "manifest": str(args.manifest),
        "manifest_sha256": sha256_file(args.manifest),
        "approvals_sha256": sha256_file(args.approvals) if args.approvals else None,
        "strict_normalization": args.strict_normalization,
        "left_url": args.left_url,
        "right_url": args.right_url,
        "case_count": len(results),
        "passed": len(results) - failed,
        "failed": failed,
        "exact_matches": exact_matches,
        "approved_difference_count": approved_difference_count,
        "unexplained_difference_count": sum(
            len(result["unexplained_differences"]) for result in results
        ),
        "approval_metadata": approval_metadata,
        "unknown_approvals": unknown_approvals,
        "features": feature_results,
        "results": results,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    if args.approval_candidates_output:
        candidates = []
        for result in results:
            for difference in result["unexplained_differences"]:
                candidate = {
                    "case_id": result["id"],
                    "kind": difference["kind"],
                    "path": difference["path"],
                    "reason": "REQUIRES_QA_REVIEW",
                    "expires_at": "REQUIRED_DATE",
                }
                if difference["kind"] == "body":
                    candidate["left_sha256"] = result["left"]["decoded_body_sha256"]
                    candidate["right_sha256"] = result["right"]["decoded_body_sha256"]
                candidates.append(candidate)
        candidate_document = {
            "schema_version": 1,
            "approved_by": "REQUIRED_QA_OWNER",
            "approval_ticket": "REQUIRED_TICKET",
            "approved_at": "REQUIRED_DATE",
            "differences": candidates,
        }
        args.approval_candidates_output.parent.mkdir(parents=True, exist_ok=True)
        args.approval_candidates_output.write_text(
            json.dumps(candidate_document, indent=2) + "\n", encoding="utf-8"
        )
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
