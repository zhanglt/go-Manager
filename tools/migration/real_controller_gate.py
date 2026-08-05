#!/usr/bin/env python3
"""Fail-closed evidence gate for RW-004 real Controller validation."""

from __future__ import annotations

import argparse
import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from contract_runner import validate_normalization
from sanitize_controller_fixture import unsafe_values


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    return parser.parse_args()


def file_digest(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def resolve_path(config_path: Path, value: str) -> Path:
    path = Path(value)
    return path if path.is_absolute() else (config_path.parent / path).resolve()


def approval_valid(value: dict[str, Any]) -> bool:
    fields = [str(value.get(name, "")).strip() for name in ("approved_by", "approval_ticket", "approved_at")]
    if not bool(value.get("approved")) or not all(fields) or any(field.startswith("REQUIRED") for field in fields):
        return False
    try:
        approved_at = datetime.fromisoformat(fields[2]).date()
    except ValueError:
        return False
    return approved_at <= datetime.now(timezone.utc).date()


def evaluate(config_path: Path, config: dict[str, Any]) -> dict[str, Any]:
    checks: list[dict[str, Any]] = []

    def add(name: str, passed: bool, actual: Any, expected: str) -> None:
        checks.append({"name": name, "passed": passed, "actual": actual, "expected": expected})

    if config.get("schema_version") != 1:
        raise ValueError("unsupported or missing evidence config schema_version")

    environment = config.get("approved_environment", {})
    add("approved Controller environment", approval_valid(environment), bool(environment.get("approved")), "true with owner/ticket/date")
    approval_id = str(environment.get("approval_id", ""))
    valid_approval_id = bool(approval_id) and not approval_id.startswith("REQUIRED")
    add("Controller approval id", valid_approval_id, approval_id or None, "non-placeholder")

    fixture_spec = config.get("sanitized_fixture", {})
    fixture_path = resolve_path(config_path, str(fixture_spec.get("path", "missing")))
    add("sanitized fixture exists", fixture_path.is_file(), str(fixture_path), "existing file")
    if fixture_path.is_file():
        fixture = json.loads(fixture_path.read_text(encoding="utf-8"))
        findings = unsafe_values(fixture)
        add("fixture sensitive-data scan", not findings, findings, "[]")
        metadata = fixture.get("sanitization", {})
        add("fixture sanitization metadata", metadata.get("schema_version") == 1, metadata.get("schema_version"), "1")
        add("fixture source approval", metadata.get("approval_id") == approval_id, metadata.get("approval_id"), approval_id)
        expected_digest = str(fixture_spec.get("sha256", ""))
        actual_digest = file_digest(fixture_path)
        add("fixture immutable digest", bool(expected_digest) and actual_digest == expected_digest, actual_digest, expected_digest or "configured sha256")
    add("fixture approval", approval_valid(fixture_spec), bool(fixture_spec.get("approved")), "true with owner/ticket/date")

    contract_spec = config.get("contract", {})
    manifest_path = resolve_path(config_path, str(contract_spec.get("manifest", "missing")))
    report_path = resolve_path(config_path, str(contract_spec.get("report", "missing")))
    coverage_path = resolve_path(config_path, str(contract_spec.get("coverage_report", "missing")))
    for label, path in (("contract manifest", manifest_path), ("contract report", report_path), ("coverage report", coverage_path)):
        add(f"{label} exists", path.is_file(), str(path), "existing file")
    if manifest_path.is_file():
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        try:
            validate_normalization(manifest)
            normalization_error = None
        except ValueError as error:
            normalization_error = str(error)
        add("strict normalization", normalization_error is None, normalization_error, "no broad/unexplained ignores")
    if report_path.is_file():
        contract = json.loads(report_path.read_text(encoding="utf-8"))
        add("contract manifest digest", manifest_path.is_file() and contract.get("manifest_sha256") == file_digest(manifest_path), contract.get("manifest_sha256"), file_digest(manifest_path) if manifest_path.is_file() else "manifest missing")
        add("contract strict mode", contract.get("strict_normalization") is True, contract.get("strict_normalization"), "true")
        minimum_cases = int(contract_spec.get("minimum_cases", 304))
        add("contract case count", int(contract.get("case_count", 0)) >= minimum_cases, contract.get("case_count"), f">= {minimum_cases}")
        add("unexplained Scala/Go differences", int(contract.get("unexplained_difference_count", -1)) == 0, contract.get("unexplained_difference_count"), "0")
        add("contract failures", int(contract.get("failed", -1)) == 0, contract.get("failed"), "0")
        add("stale/unknown difference approvals", not contract.get("unknown_approvals"), contract.get("unknown_approvals"), "[]")
        feature_results = contract.get("features", {})
        for feature in config.get("required_features", []):
            add(f"contract feature: {feature}", bool(feature_results.get(feature, {}).get("passed")), feature_results.get(feature), "passed")
    if coverage_path.is_file():
        coverage = json.loads(coverage_path.read_text(encoding="utf-8"))
        add("route coverage", coverage.get("covered_route_count") == coverage.get("route_count") == 263, f"{coverage.get('covered_route_count')}/{coverage.get('route_count')}", "263/263")
        add("coverage ambiguity", not any(int(coverage.get(name, -1)) for name in ("missing_route_count", "ambiguous_case_count", "unknown_case_count")), {name: coverage.get(name) for name in ("missing_route_count", "ambiguous_case_count", "unknown_case_count")}, "all zero")

    workflow_spec = config.get("workflows", {})
    workflow_path = resolve_path(config_path, str(workflow_spec.get("report", "missing")))
    add("workflow report exists", workflow_path.is_file(), str(workflow_path), "existing file")
    if workflow_path.is_file():
        workflow_report = json.loads(workflow_path.read_text(encoding="utf-8"))
        workflows = {str(item.get("id")): item for item in workflow_report.get("workflows", [])}
        add("workflow Go backend probe", bool(workflow_report.get("go_backend_verified")), workflow_report.get("go_backend_verified"), "true")
        for workflow_id in workflow_spec.get("required", []):
            workflow = workflows.get(str(workflow_id), {})
            passed = workflow.get("status") == "passed" and workflow.get("go_backend") is True
            add(f"workflow: {workflow_id}", passed, workflow.get("status", "missing"), "passed on Go backend")

    add("QA approval", approval_valid(config.get("qa_approval", {})), bool(config.get("qa_approval", {}).get("approved")), "true with owner/ticket/date")
    add("UI Owner approval", approval_valid(config.get("ui_owner_approval", {})), bool(config.get("ui_owner_approval", {}).get("approved")), "true with owner/ticket/date")

    return {
        "schema_version": 1,
        "evaluated_at": datetime.now(timezone.utc).isoformat(),
        "passed": bool(checks) and all(check["passed"] for check in checks),
        "checks": checks,
    }


def main() -> int:
    args = parse_args()
    config_path = args.config.resolve()
    result = evaluate(config_path, json.loads(config_path.read_text(encoding="utf-8")))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
