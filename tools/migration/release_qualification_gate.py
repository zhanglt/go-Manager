#!/usr/bin/env python3
"""Fail-closed G5 gate for an immutable Go Manager release candidate."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


REQUIRED_COVERAGE = {
    "contract": {"routes", "errors", "gzip", "headers", "cookies"},
    "e2e": {
        "login",
        "dashboard",
        "workloads",
        "network-policy",
        "risk",
        "settings",
        "cli",
        "federation",
    },
    "performance": {"idle", "steady", "burst", "large-body", "upload", "download"},
    "stability": {"24h", "controller-fault", "resource-leaks"},
    "security": {
        "tls",
        "csp",
        "dependencies",
        "sbom",
        "provenance",
        "secrets",
        "file-permissions",
    },
    "fips": {"runtime-mode", "toolchain", "base-image", "linux/amd64", "linux/arm64"},
    "upgrade": {"configuration", "certificates", "cli", "ui", "session"},
    "rollback": {"scala-restore", "readiness", "recovery-time", "session"},
}
REQUIRED_APPROVALS = ("qa", "security_fips", "performance", "release", "product")
DIGEST_PATTERN = re.compile(r"^sha256:[0-9a-f]{64}$")
COMMIT_PATTERN = re.compile(r"^[0-9a-f]{7,40}$")
CLOSED_DEFECT_STATES = {"closed", "resolved", "verified"}
DEFECT_SEVERITIES = {"P0", "P1", "P2", "P3", "P4"}


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
    return f"sha256:{digest.hexdigest()}"


def resolve_path(config_path: Path, value: str) -> Path:
    path = Path(value)
    return path if path.is_absolute() else (config_path.parent / path).resolve()


def parse_time(value: Any) -> datetime | None:
    text = str(value or "").strip()
    if not text or text.startswith("REQUIRED"):
        return None
    try:
        parsed = datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def real_text(value: Any) -> bool:
    text = str(value or "").strip()
    return bool(text) and not text.startswith("REQUIRED")


def fingerprint_payload(config: dict[str, Any]) -> dict[str, Any]:
    evidence = []
    for item in config.get("evidence", []):
        evidence.append(
            {
                name: item.get(name)
                for name in (
                    "id",
                    "category",
                    "sha256",
                    "status",
                    "image_digests",
                    "coverage",
                    "executed_at",
                    "environment",
                    "approval_ticket",
                )
            }
        )
    return {
        "schema_version": config.get("schema_version"),
        "candidate": config.get("candidate"),
        "evidence": evidence,
        "defects": config.get("defects", []),
        "accepted_limitations": config.get("accepted_limitations", []),
    }


def qualification_digest(config: dict[str, Any]) -> str:
    encoded = json.dumps(
        fingerprint_payload(config), sort_keys=True, separators=(",", ":"), ensure_ascii=True
    ).encode()
    return f"sha256:{hashlib.sha256(encoded).hexdigest()}"


def evaluate(config_path: Path, config: dict[str, Any]) -> dict[str, Any]:
    checks: list[dict[str, Any]] = []

    def add(name: str, passed: bool, actual: Any, expected: str) -> None:
        checks.append({"name": name, "passed": passed, "actual": actual, "expected": expected})

    if config.get("schema_version") != 1:
        raise ValueError("unsupported or missing evidence config schema_version")

    candidate = config.get("candidate", {})
    built_at = parse_time(candidate.get("built_at"))
    add("candidate version", real_text(candidate.get("version")), candidate.get("version"), "non-placeholder")
    commit = str(candidate.get("commit", ""))
    add("candidate commit", bool(COMMIT_PATTERN.fullmatch(commit)), commit or None, "7-40 lowercase hex")
    add(
        "candidate build timestamp",
        built_at is not None and built_at <= datetime.now(timezone.utc),
        candidate.get("built_at"),
        "valid timestamp not in the future",
    )

    images: dict[str, str] = {}
    for image_name in ("image", "fips_image"):
        image = candidate.get(image_name, {})
        digest = str(image.get("digest", ""))
        reference = str(image.get("reference", ""))
        platforms = image.get("platforms", {})
        images[image_name] = digest
        add(
            f"{image_name} immutable digest",
            bool(DIGEST_PATTERN.fullmatch(digest)),
            digest or None,
            "sha256:<64 lowercase hex>",
        )
        reference_valid = real_text(reference.partition("@")[0]) and reference.endswith(f"@{digest}")
        add(
            f"{image_name} digest reference",
            reference_valid,
            reference or None,
            "reference pinned with @digest",
        )
        for platform in ("linux/amd64", "linux/arm64"):
            platform_digest = str(platforms.get(platform, ""))
            add(
                f"{image_name} platform: {platform}",
                bool(DIGEST_PATTERN.fullmatch(platform_digest)),
                platform_digest or None,
                "sha256:<64 lowercase hex>",
            )
    add(
        "normal and FIPS images are distinct",
        bool(images.get("image")) and images.get("image") != images.get("fips_image"),
        images,
        "different immutable digests",
    )

    evidence_items = config.get("evidence", [])
    evidence_ids: list[str] = []
    evidence_times: list[datetime] = []
    coverage_by_category = {category: set() for category in REQUIRED_COVERAGE}
    category_counts = {category: 0 for category in REQUIRED_COVERAGE}
    for index, item in enumerate(evidence_items):
        evidence_id = str(item.get("id", ""))
        category = str(item.get("category", ""))
        label = evidence_id or f"evidence[{index}]"
        evidence_ids.append(evidence_id)
        add(f"{label}: id", real_text(evidence_id), evidence_id or None, "non-placeholder")
        add(f"{label}: category", category in REQUIRED_COVERAGE, category or None, "required category")
        report_path = resolve_path(config_path, str(item.get("path", "missing")))
        report_exists = report_path.is_file()
        add(f"{label}: report exists", report_exists, str(report_path), "existing file")
        actual_digest = file_digest(report_path) if report_exists else None
        expected_digest = str(item.get("sha256", ""))
        add(
            f"{label}: report digest",
            bool(DIGEST_PATTERN.fullmatch(expected_digest)) and actual_digest == expected_digest,
            actual_digest,
            expected_digest or "configured sha256",
        )
        add(f"{label}: passed", item.get("status") == "passed", item.get("status"), "passed")
        subject_digests = item.get("image_digests", [])
        required_digests = {images.get("fips_image")} if category == "fips" else {images.get("image")}
        add(
            f"{label}: candidate binding",
            all(digest in subject_digests for digest in required_digests if digest),
            subject_digests,
            f"contains {sorted(digest for digest in required_digests if digest)}",
        )
        executed_at = parse_time(item.get("executed_at"))
        if executed_at is not None:
            evidence_times.append(executed_at)
        current_time = datetime.now(timezone.utc)
        timestamp_valid = executed_at is not None and executed_at <= current_time
        if built_at is not None and executed_at is not None:
            timestamp_valid = timestamp_valid and executed_at >= built_at
        add(
            f"{label}: execution timestamp",
            timestamp_valid,
            item.get("executed_at"),
            "valid timestamp between candidate build and now",
        )
        add(
            f"{label}: environment",
            real_text(item.get("environment")),
            item.get("environment"),
            "non-placeholder",
        )
        add(
            f"{label}: traceability",
            real_text(item.get("approval_ticket")),
            item.get("approval_ticket"),
            "non-placeholder ticket",
        )
        if category in REQUIRED_COVERAGE:
            category_counts[category] += 1
            coverage_by_category[category].update(str(value) for value in item.get("coverage", []))

    add("unique evidence ids", len(evidence_ids) == len(set(evidence_ids)), evidence_ids, "all unique")
    for category, required in REQUIRED_COVERAGE.items():
        add(f"{category}: evidence present", category_counts[category] > 0, category_counts[category], "> 0")
        missing = sorted(required - coverage_by_category[category])
        add(f"{category}: required coverage", not missing, missing, "[]")

    defects = config.get("defects", [])
    unresolved_blockers = []
    for defect in defects:
        if not all(real_text(defect.get(name)) for name in ("id", "severity", "status")):
            unresolved_blockers.append({"id": defect.get("id"), "reason": "incomplete record"})
            continue
        severity = str(defect["severity"]).upper()
        status = str(defect["status"]).lower()
        if severity not in DEFECT_SEVERITIES:
            unresolved_blockers.append(
                {"id": defect["id"], "severity": severity, "reason": "unknown severity"}
            )
            continue
        if severity in {"P0", "P1"} and status not in CLOSED_DEFECT_STATES:
            unresolved_blockers.append({"id": defect["id"], "severity": severity, "status": status})
    add("no unresolved P0/P1 defects", not unresolved_blockers, unresolved_blockers, "[]")

    invalid_limitations = []
    limitation_ids = []
    for limitation in config.get("accepted_limitations", []):
        limitation_ids.append(str(limitation.get("id", "")))
        required_fields = (
            "id",
            "impact",
            "workaround",
            "owner",
            "target_version",
            "approval_ticket",
        )
        valid = all(real_text(limitation.get(name)) for name in required_fields)
        valid = valid and limitation.get("accepted") is True
        accepted_at = parse_time(limitation.get("accepted_at"))
        valid = valid and accepted_at is not None and accepted_at <= datetime.now(timezone.utc)
        if not valid:
            invalid_limitations.append(limitation.get("id") or "missing-id")
    add("accepted limitation records", not invalid_limitations, invalid_limitations, "[]")
    add(
        "unique limitation ids",
        len(limitation_ids) == len(set(limitation_ids)),
        limitation_ids,
        "all unique",
    )

    fingerprint = qualification_digest(config)
    approvals = config.get("approvals", {})
    latest_evidence_at = max(evidence_times) if evidence_times else None
    for role in REQUIRED_APPROVALS:
        approval = approvals.get(role, {})
        approved_at = parse_time(approval.get("approved_at"))
        valid = (
            approval.get("approved") is True
            and all(real_text(approval.get(name)) for name in ("approved_by", "approval_ticket"))
            and approved_at is not None
            and approved_at <= datetime.now(timezone.utc)
            and (latest_evidence_at is None or approved_at >= latest_evidence_at)
            and approval.get("image_digest") == images.get("image")
            and approval.get("fips_image_digest") == images.get("fips_image")
            and approval.get("qualification_digest") == fingerprint
        )
        add(
            f"approval: {role}",
            valid,
            {
                "approved": approval.get("approved"),
                "approved_by": approval.get("approved_by"),
                "approval_ticket": approval.get("approval_ticket"),
                "approved_at": approval.get("approved_at"),
                "image_digest": approval.get("image_digest"),
                "fips_image_digest": approval.get("fips_image_digest"),
                "qualification_digest": approval.get("qualification_digest"),
            },
            "valid owner/ticket/date bound to both images and qualification digest",
        )

    return {
        "schema_version": 1,
        "evaluated_at": datetime.now(timezone.utc).isoformat(),
        "qualification_digest": fingerprint,
        "candidate": {
            "version": candidate.get("version"),
            "commit": candidate.get("commit"),
            "image_digest": images.get("image"),
            "fips_image_digest": images.get("fips_image"),
        },
        "passed": bool(checks) and all(check["passed"] for check in checks),
        "checks": checks,
    }


def main() -> int:
    args = parse_args()
    config_path = args.config.resolve()
    result = evaluate(config_path, json.loads(config_path.read_text(encoding="utf-8")))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(f"qualification_digest={result['qualification_digest']}")
    print("RW-010 release qualification: " + ("PASS" if result["passed"] else "FAIL"))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
