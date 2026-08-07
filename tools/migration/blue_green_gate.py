#!/usr/bin/env python3
"""Fail-closed evidence gate for the RW-011 blue/green cutover and rollback."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


SCHEMA_VERSION = 1
DIGEST_PATTERN = re.compile(r"^sha256:[0-9a-f]{64}$")
REQUIRED_BACKUPS = ("configuration", "certificates", "deployment")
REQUIRED_PREFLIGHT = ("readiness", "certificate", "version", "static_ui", "controller", "shadow")
REQUIRED_THRESHOLDS = {
    "http_5xx_rate": "max",
    "login_success_rate": "min",
    "p95_ms": "max",
    "rss_mib": "max",
    "cpu_percent": "max",
    "goroutines": "max",
    "cache_utilization": "max",
    "controller_error_rate": "max",
    "active_file_jobs": "max",
    "active_command_jobs": "max",
}
REQUIRED_APPROVALS = ("operations", "release")


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


def resolve_path(config_path: Path, value: Any) -> Path:
    path = Path(str(value or "missing"))
    return path if path.is_absolute() else (config_path.parent / path).resolve()


def immutable_reference(reference: Any, digest: Any) -> bool:
    reference_text = str(reference or "")
    digest_text = str(digest or "")
    return (
        bool(DIGEST_PATTERN.fullmatch(digest_text))
        and real_text(reference_text.partition("@")[0])
        and reference_text.endswith(f"@{digest_text}")
    )


def fingerprint_payload(config: dict[str, Any]) -> dict[str, Any]:
    return {
        name: config.get(name)
        for name in ("schema_version", "change", "candidate", "backups", "rollback_runbook", "monitoring", "reports")
    }


def execution_digest(config: dict[str, Any]) -> str:
    encoded = json.dumps(
        fingerprint_payload(config), sort_keys=True, separators=(",", ":"), ensure_ascii=True
    ).encode()
    return f"sha256:{hashlib.sha256(encoded).hexdigest()}"


def numeric(value: Any) -> bool:
    return not isinstance(value, bool) and isinstance(value, (int, float)) and math.isfinite(value)


def threshold_triggers(samples: list[dict[str, Any]], rules: list[dict[str, Any]]) -> list[dict[str, Any]]:
    triggers = []
    for rule in rules:
        metric = str(rule.get("metric", ""))
        direction = str(rule.get("direction", ""))
        limit = rule.get("value")
        sustain = rule.get("sustain_seconds")
        if direction not in {"max", "min"} or not numeric(limit) or not numeric(sustain) or sustain < 0:
            continue
        breach_started: datetime | None = None
        for sample in samples:
            captured_at = parse_time(sample.get("captured_at"))
            actual = sample.get(metric)
            if captured_at is None or not numeric(actual):
                breach_started = None
                continue
            breached = actual > limit if direction == "max" else actual < limit
            if not breached:
                breach_started = None
                continue
            if breach_started is None:
                breach_started = captured_at
            if (captured_at - breach_started).total_seconds() >= sustain:
                triggers.append(
                    {
                        "rule_id": rule.get("id"),
                        "metric": metric,
                        "detected_at": captured_at.isoformat(),
                        "actual": actual,
                        "limit": limit,
                    }
                )
                break
    return triggers


def evaluate(config_path: Path, config: dict[str, Any]) -> dict[str, Any]:
    checks: list[dict[str, Any]] = []
    evidence_times: list[datetime] = []

    def add(name: str, passed: bool, actual: Any, expected: str) -> None:
        checks.append({"name": name, "passed": bool(passed), "actual": actual, "expected": expected})

    def read_artifact(label: str, spec: dict[str, Any], parse_json: bool = False) -> Any:
        path = resolve_path(config_path, spec.get("path"))
        exists = path.is_file()
        add(f"{label}: exists", exists, str(path), "existing file")
        expected_digest = str(spec.get("sha256", ""))
        actual_digest = file_digest(path) if exists else None
        add(
            f"{label}: digest",
            bool(DIGEST_PATTERN.fullmatch(expected_digest)) and expected_digest == actual_digest,
            actual_digest,
            expected_digest or "configured sha256",
        )
        if not exists or not parse_json:
            return path if exists else None
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
            add(f"{label}: JSON", False, type(error).__name__, "valid JSON object")
            return None

    if config.get("schema_version") != SCHEMA_VERSION:
        raise ValueError("unsupported or missing blue/green evidence schema_version")

    now = datetime.now(timezone.utc)
    change = config.get("change", {})
    window_start = parse_time(change.get("window_start"))
    window_end = parse_time(change.get("window_end"))
    approved_at = parse_time(change.get("approved_at"))
    add("change id", real_text(change.get("id")), change.get("id"), "non-placeholder")
    add("change environment", real_text(change.get("environment")), change.get("environment"), "non-placeholder")
    add("change ticket", real_text(change.get("ticket")), change.get("ticket"), "non-placeholder")
    add("change approver", real_text(change.get("approved_by")), change.get("approved_by"), "non-placeholder")
    add(
        "approved change window",
        approved_at is not None
        and window_start is not None
        and window_end is not None
        and approved_at <= window_start < window_end <= now,
        {
            "approved_at": change.get("approved_at"),
            "window_start": change.get("window_start"),
            "window_end": change.get("window_end"),
        },
        "approval <= start < end <= now",
    )

    candidate = config.get("candidate", {})
    digests: dict[str, str] = {}
    for name in ("go_image", "scala_image"):
        image = candidate.get(name, {})
        digest = str(image.get("digest", ""))
        digests[name] = digest
        add(
            f"{name} immutable reference",
            immutable_reference(image.get("reference"), digest),
            image,
            "registry reference pinned with @sha256 digest",
        )
    add(
        "blue and green images are distinct",
        bool(digests.get("go_image")) and digests.get("go_image") != digests.get("scala_image"),
        digests,
        "different immutable digests",
    )

    qualification = read_artifact(
        "RW-010 qualification report", candidate.get("qualification_report", {}), parse_json=True
    )
    qualification_time = parse_time(qualification.get("evaluated_at")) if isinstance(qualification, dict) else None
    if qualification_time is not None:
        evidence_times.append(qualification_time)
    add(
        "RW-010 qualification passed",
        isinstance(qualification, dict)
        and qualification.get("schema_version") == 1
        and qualification.get("passed") is True
        and qualification.get("candidate", {}).get("image_digest") == digests.get("go_image"),
        qualification.get("candidate") if isinstance(qualification, dict) else None,
        "passed report bound to Go candidate digest",
    )

    backups = config.get("backups", {})
    for name in REQUIRED_BACKUPS:
        spec = backups.get(name, {})
        read_artifact(f"{name} backup", spec)
        created_at = parse_time(spec.get("created_at"))
        if created_at is not None:
            evidence_times.append(created_at)
        add(
            f"{name} backup controls",
            created_at is not None
            and window_start is not None
            and created_at <= window_start
            and spec.get("protected") is True
            and spec.get("restore_tested") is True,
            {
                "created_at": spec.get("created_at"),
                "protected": spec.get("protected"),
                "restore_tested": spec.get("restore_tested"),
            },
            "created before cutover, protected and restore-tested",
        )

    runbook = config.get("rollback_runbook", {})
    read_artifact("rollback runbook", runbook)
    add(
        "rollback runbook approval",
        real_text(runbook.get("owner")) and real_text(runbook.get("approval_ticket")),
        {"owner": runbook.get("owner"), "approval_ticket": runbook.get("approval_ticket")},
        "owner and approval ticket",
    )

    reports = config.get("reports", {})
    preflight = read_artifact("green preflight report", reports.get("preflight", {}), parse_json=True)
    preflight_time = parse_time(preflight.get("executed_at")) if isinstance(preflight, dict) else None
    if preflight_time is not None:
        evidence_times.append(preflight_time)
    preflight_checks = preflight.get("checks", {}) if isinstance(preflight, dict) else {}
    add(
        "green preflight candidate binding",
        isinstance(preflight, dict)
        and preflight.get("schema_version") == 1
        and preflight.get("candidate_digest") == digests.get("go_image"),
        preflight.get("candidate_digest") if isinstance(preflight, dict) else None,
        digests.get("go_image") or "Go digest",
    )
    for name in REQUIRED_PREFLIGHT:
        check = preflight_checks.get(name, {})
        add(f"green preflight: {name}", check.get("passed") is True, check, "passed")
    readiness = preflight_checks.get("readiness", {})
    add(
        "green readiness response",
        readiness.get("status_code") == 200,
        readiness.get("status_code"),
        "200",
    )
    certificate = preflight_checks.get("certificate", {})
    certificate_expires = parse_time(certificate.get("expires_at"))
    add(
        "green certificate verification",
        certificate.get("verified") is True
        and certificate_expires is not None
        and window_end is not None
        and certificate_expires > window_end,
        certificate,
        "verified certificate valid beyond the cutover window",
    )
    version = preflight_checks.get("version", {})
    add(
        "green version response",
        real_text(version.get("actual")) and version.get("actual") == version.get("expected"),
        version,
        "non-placeholder actual equals expected",
    )
    for name in ("static_ui", "controller"):
        check = preflight_checks.get(name, {})
        valid = (
            numeric(check.get("requests"))
            and check.get("requests") > 0
            and numeric(check.get("errors"))
            and check.get("errors") == 0
        )
        add(f"green {name} traffic", valid, check, "requests > 0 and errors = 0")
    shadow = preflight_checks.get("shadow", {})
    add(
        "read-only shadow safety",
        shadow.get("read_only") is True
        and numeric(shadow.get("requests"))
        and shadow.get("requests") > 0
        and numeric(shadow.get("mutation_requests"))
        and shadow.get("mutation_requests") == 0
        and numeric(shadow.get("unapproved_differences"))
        and shadow.get("unapproved_differences") == 0,
        shadow,
        "read-only requests > 0, no mutations or unapproved differences",
    )

    cutover = read_artifact("cutover report", reports.get("cutover", {}), parse_json=True)
    cutover_started = parse_time(cutover.get("started_at")) if isinstance(cutover, dict) else None
    switched_at = parse_time(cutover.get("switched_at")) if isinstance(cutover, dict) else None
    cutover_completed = parse_time(cutover.get("completed_at")) if isinstance(cutover, dict) else None
    if cutover_completed is not None:
        evidence_times.append(cutover_completed)
    add(
        "cutover timeline",
        window_start is not None
        and window_end is not None
        and preflight_time is not None
        and cutover_started is not None
        and switched_at is not None
        and cutover_completed is not None
        and preflight_time <= cutover_started <= switched_at <= cutover_completed
        and window_start <= cutover_started
        and cutover_completed <= window_end,
        {
            "started_at": cutover.get("started_at") if isinstance(cutover, dict) else None,
            "switched_at": cutover.get("switched_at") if isinstance(cutover, dict) else None,
            "completed_at": cutover.get("completed_at") if isinstance(cutover, dict) else None,
        },
        "preflight <= start <= switch <= complete inside approved window",
    )
    add(
        "cutover target binding",
        isinstance(cutover, dict)
        and cutover.get("schema_version") == 1
        and cutover.get("initial_target_digest") == digests.get("scala_image")
        and cutover.get("final_target_digest") == digests.get("go_image"),
        {
            "initial": cutover.get("initial_target_digest") if isinstance(cutover, dict) else None,
            "final": cutover.get("final_target_digest") if isinstance(cutover, dict) else None,
        },
        "Scala digest -> Go digest",
    )
    add(
        "cutover side-effect safety",
        isinstance(cutover, dict)
        and cutover.get("new_connections_only") is True
        and cutover.get("side_effect_replay") is False
        and numeric(cutover.get("duplicated_mutating_requests"))
        and cutover.get("duplicated_mutating_requests") == 0,
        {
            "new_connections_only": cutover.get("new_connections_only") if isinstance(cutover, dict) else None,
            "side_effect_replay": cutover.get("side_effect_replay") if isinstance(cutover, dict) else None,
            "duplicated_mutating_requests": cutover.get("duplicated_mutating_requests")
            if isinstance(cutover, dict)
            else None,
        },
        "new connections only and zero replayed/duplicated mutations",
    )

    monitoring = config.get("monitoring", {})
    monitoring_approved_at = parse_time(monitoring.get("approved_at"))
    add(
        "monitoring policy approval",
        real_text(monitoring.get("approved_by"))
        and real_text(monitoring.get("approval_ticket"))
        and monitoring_approved_at is not None
        and window_start is not None
        and monitoring_approved_at <= window_start,
        {
            "approved_by": monitoring.get("approved_by"),
            "approval_ticket": monitoring.get("approval_ticket"),
            "approved_at": monitoring.get("approved_at"),
        },
        "approved before cutover window",
    )
    rules = monitoring.get("thresholds", [])
    rule_ids = [str(rule.get("id", "")) for rule in rules]
    rule_metrics = [str(rule.get("metric", "")) for rule in rules]
    add(
        "unique threshold ids",
        len(rule_ids) == len(set(rule_ids)) and all(map(real_text, rule_ids)),
        rule_ids,
        "unique",
    )
    add(
        "threshold metric set",
        len(rule_metrics) == len(REQUIRED_THRESHOLDS)
        and len(rule_metrics) == len(set(rule_metrics))
        and set(rule_metrics) == set(REQUIRED_THRESHOLDS),
        rule_metrics,
        "exactly one rule for each required metric",
    )
    rules_by_metric = {str(rule.get("metric", "")): rule for rule in rules}
    for metric, direction in REQUIRED_THRESHOLDS.items():
        rule = rules_by_metric.get(metric, {})
        valid = (
            rule.get("direction") == direction
            and numeric(rule.get("value"))
            and numeric(rule.get("sustain_seconds"))
            and rule.get("sustain_seconds") >= 0
            and rule.get("automatic_rollback") is True
        )
        add(f"rollback threshold: {metric}", valid, rule, f"{direction}, numeric, automatic rollback")

    observation = read_artifact("observation report", reports.get("observation", {}), parse_json=True)
    observation_start = parse_time(observation.get("started_at")) if isinstance(observation, dict) else None
    observation_end = parse_time(observation.get("ended_at")) if isinstance(observation, dict) else None
    if observation_end is not None:
        evidence_times.append(observation_end)
    minimum_observation = monitoring.get("minimum_observation_seconds")
    observation_duration = (
        (observation_end - observation_start).total_seconds()
        if observation_start is not None and observation_end is not None
        else -1
    )
    samples = observation.get("samples", []) if isinstance(observation, dict) else []
    max_interval = monitoring.get("max_sample_interval_seconds")
    sample_times = [parse_time(sample.get("captured_at")) for sample in samples]
    samples_valid = bool(samples) and all(timestamp is not None for timestamp in sample_times)
    samples_valid = samples_valid and all(
        sample.get("target_digest") == digests.get("go_image")
        and all(numeric(sample.get(metric)) for metric in REQUIRED_THRESHOLDS)
        for sample in samples
    )
    ordered_times = [timestamp for timestamp in sample_times if timestamp is not None]
    samples_valid = samples_valid and ordered_times == sorted(ordered_times)
    gaps = [
        (right - left).total_seconds() for left, right in zip(ordered_times, ordered_times[1:])
    ]
    if observation_start is not None and observation_end is not None and ordered_times:
        gaps.extend(
            [
                (ordered_times[0] - observation_start).total_seconds(),
                (observation_end - ordered_times[-1]).total_seconds(),
            ]
        )
    sample_boundaries_valid = (
        observation_start is not None
        and observation_end is not None
        and bool(ordered_times)
        and observation_start <= ordered_times[0] <= ordered_times[-1] <= observation_end
    )
    interval_valid = (
        numeric(max_interval)
        and max_interval > 0
        and bool(gaps)
        and min(gaps) >= 0
        and max(gaps) <= max_interval
    )
    observed_triggers = threshold_triggers(samples, rules)
    add(
        "observation window",
        switched_at is not None
        and window_end is not None
        and observation_start is not None
        and observation_end is not None
        and numeric(minimum_observation)
        and minimum_observation > 0
        and switched_at <= observation_start < observation_end <= window_end
        and (observation_duration >= minimum_observation or bool(observed_triggers)),
        observation_duration,
        f">= {minimum_observation} seconds, unless an automatic rollback threshold triggers",
    )
    add(
        "observation metric samples",
        samples_valid and sample_boundaries_valid and interval_valid,
        {"count": len(samples), "max_gap_seconds": max(gaps) if gaps else None},
        "ordered, candidate-bound, complete metrics within sampling interval",
    )
    add(
        "observation critical safety",
        isinstance(observation, dict)
        and observation.get("schema_version") == 1
        and observation.get("critical_path_failures") == []
        and observation.get("security_incidents") == []
        and numeric(observation.get("certificate_errors"))
        and observation.get("certificate_errors") == 0
        and numeric(observation.get("fips_mode_errors"))
        and observation.get("fips_mode_errors") == 0
        and observation.get("controller_amplification") is False,
        {
            "critical_path_failures": observation.get("critical_path_failures")
            if isinstance(observation, dict)
            else None,
            "security_incidents": observation.get("security_incidents")
            if isinstance(observation, dict)
            else None,
            "certificate_errors": observation.get("certificate_errors")
            if isinstance(observation, dict)
            else None,
            "fips_mode_errors": observation.get("fips_mode_errors")
            if isinstance(observation, dict)
            else None,
            "controller_amplification": observation.get("controller_amplification")
            if isinstance(observation, dict)
            else None,
        },
        "no critical/security/certificate/FIPS/amplification failures",
    )
    triggers = observed_triggers if samples_valid else []

    max_recovery = monitoring.get("max_recovery_seconds")

    def validate_rollback(label: str, report: Any, injected: bool) -> bool:
        triggered_at = parse_time(report.get("triggered_at")) if isinstance(report, dict) else None
        traffic_restored_at = (
            parse_time(report.get("traffic_restored_at")) if isinstance(report, dict) else None
        )
        scala_ready_at = parse_time(report.get("scala_ready_at")) if isinstance(report, dict) else None
        completed_at = parse_time(report.get("completed_at")) if isinstance(report, dict) else None
        if completed_at is not None:
            evidence_times.append(completed_at)
        recovery = (
            (scala_ready_at - triggered_at).total_seconds()
            if triggered_at is not None and scala_ready_at is not None
            else -1
        )
        timeline_valid = (
            triggered_at is not None
            and traffic_restored_at is not None
            and scala_ready_at is not None
            and completed_at is not None
            and triggered_at <= traffic_restored_at <= scala_ready_at <= completed_at <= now
            and monitoring_approved_at is not None
            and monitoring_approved_at <= triggered_at
        )
        content_valid = (
            isinstance(report, dict)
            and report.get("schema_version") == 1
            and report.get("candidate_digest") == digests.get("go_image")
            and report.get("scala_digest") == digests.get("scala_image")
            and report.get("automatic") is True
            and report.get("injected") is injected
            and report.get("readiness_passed") is True
            and report.get("no_data_rollback") is True
            and report.get("final_target_digest") == digests.get("scala_image")
            and report.get("session_behavior") in {"preserved", "relogin-required"}
            and real_text(report.get("notification_ticket"))
            and report.get("trigger_rule_id") in rule_ids
        )
        recovery_valid = (
            numeric(max_recovery)
            and max_recovery > 0
            and recovery >= 0
            and recovery <= max_recovery
            and numeric(report.get("recovery_seconds"))
            and abs(report.get("recovery_seconds") - recovery) < 0.001
        ) if isinstance(report, dict) else False
        add(
            f"{label}: recovery",
            timeline_valid and content_valid and recovery_valid,
            {"recovery_seconds": recovery, "report": report},
            "automatic Scala restore and readiness within approved recovery threshold",
        )
        return timeline_valid and content_valid and recovery_valid

    rollback_drill = read_artifact(
        "rollback drill report", reports.get("rollback_drill", {}), parse_json=True
    )
    drill_valid = validate_rollback("rollback drill", rollback_drill, injected=True)

    live_rollback_valid = False
    if triggers:
        live_rollback = read_artifact(
            "live rollback report", reports.get("live_rollback", {}), parse_json=True
        )
        live_rollback_valid = validate_rollback("live rollback", live_rollback, injected=False)
        detected_ids = {trigger["rule_id"] for trigger in triggers}
        trigger_id = live_rollback.get("trigger_rule_id") if isinstance(live_rollback, dict) else None
        rollback_triggered_at = (
            parse_time(live_rollback.get("triggered_at")) if isinstance(live_rollback, dict) else None
        )
        detected_at = min(
            (
                parse_time(trigger["detected_at"])
                for trigger in triggers
                if trigger["rule_id"] == trigger_id
            ),
            default=None,
        )
        trigger_timing_valid = (
            detected_at is not None
            and rollback_triggered_at is not None
            and numeric(max_interval)
            and detected_at <= rollback_triggered_at
            and (rollback_triggered_at - detected_at).total_seconds() <= max_interval
        )
        add(
            "live rollback matched detected threshold",
            trigger_id in detected_ids and trigger_timing_valid,
            {
                "trigger_id": trigger_id,
                "detected": sorted(detected_ids),
                "detected_at": detected_at.isoformat() if detected_at else None,
                "rollback_triggered_at": live_rollback.get("triggered_at")
                if isinstance(live_rollback, dict)
                else None,
            },
            "matching threshold and automatic rollback start within one sample interval",
        )
    add(
        "cutover observation outcome",
        not triggers or live_rollback_valid,
        triggers,
        "no sustained threshold breaches, or successful automatic live rollback",
    )
    add("rollback drill completed", drill_valid, drill_valid, "true")

    fingerprint = execution_digest(config)
    latest_evidence = max(evidence_times) if evidence_times else None
    approvals = config.get("approvals", {})
    for role in REQUIRED_APPROVALS:
        approval = approvals.get(role, {})
        signed_at = parse_time(approval.get("approved_at"))
        valid = (
            approval.get("approved") is True
            and real_text(approval.get("approved_by"))
            and real_text(approval.get("approval_ticket"))
            and signed_at is not None
            and signed_at <= now
            and (latest_evidence is None or signed_at >= latest_evidence)
            and approval.get("go_image_digest") == digests.get("go_image")
            and approval.get("scala_image_digest") == digests.get("scala_image")
            and approval.get("execution_digest") == fingerprint
        )
        add(
            f"approval: {role}",
            valid,
            approval,
            "post-execution approval bound to both image digests and execution digest",
        )

    return {
        "schema_version": SCHEMA_VERSION,
        "evaluated_at": now.isoformat(),
        "execution_digest": fingerprint,
        "candidate": {
            "go_image_digest": digests.get("go_image"),
            "scala_image_digest": digests.get("scala_image"),
        },
        "detected_threshold_triggers": triggers,
        "passed": bool(checks) and all(check["passed"] for check in checks),
        "checks": checks,
    }


def main() -> int:
    args = parse_args()
    config_path = args.config.resolve()
    config = json.loads(config_path.read_text(encoding="utf-8"))
    result = evaluate(config_path, config)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(f"execution_digest={result['execution_digest']}")
    print("RW-011 blue/green cutover: " + ("PASS" if result["passed"] else "FAIL"))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
