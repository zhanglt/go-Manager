#!/usr/bin/env python3

import json
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

from blue_green_gate import (
    REQUIRED_APPROVALS,
    REQUIRED_THRESHOLDS,
    evaluate,
    execution_digest,
    file_digest,
)


GO_DIGEST = "sha256:" + "1" * 64
SCALA_DIGEST = "sha256:" + "2" * 64


def timestamp(minutes: int, seconds: int = 0) -> str:
    value = datetime(2026, 8, 6, 1, 0, tzinfo=timezone.utc)
    return (value + timedelta(minutes=minutes, seconds=seconds)).isoformat().replace("+00:00", "Z")


class BlueGreenGateTest(unittest.TestCase):
    def write_json(self, path: Path, value: dict) -> dict:
        path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
        return {"path": path.name, "sha256": file_digest(path)}

    def sign(self, config: dict) -> None:
        fingerprint = execution_digest(config)
        config["approvals"] = {
            role: {
                "approved": True,
                "approved_by": f"{role}-owner",
                "approval_ticket": f"RW011-{role}",
                "approved_at": timestamp(30),
                "go_image_digest": GO_DIGEST,
                "scala_image_digest": SCALA_DIGEST,
                "execution_digest": fingerprint,
            }
            for role in REQUIRED_APPROVALS
        }

    def update_report(self, root: Path, config: dict, name: str, report: dict) -> None:
        config["reports"][name] = self.write_json(root / f"{name}.json", report)
        self.sign(config)

    def complete_config(self, root: Path) -> tuple[dict, dict[str, dict]]:
        qualification = {
            "schema_version": 1,
            "passed": True,
            "evaluated_at": timestamp(-50),
            "candidate": {"image_digest": GO_DIGEST},
        }
        qualification_spec = self.write_json(root / "qualification.json", qualification)

        backups = {}
        for name in ("configuration", "certificates", "deployment"):
            path = root / f"{name}.backup"
            path.write_text(f"synthetic {name} backup\n", encoding="utf-8")
            backups[name] = {
                "path": path.name,
                "sha256": file_digest(path),
                "created_at": timestamp(-40),
                "protected": True,
                "restore_tested": True,
            }
        runbook_path = root / "rollback-runbook.md"
        runbook_path.write_text("Synthetic rollback runbook.\n", encoding="utf-8")

        preflight = {
            "schema_version": 1,
            "candidate_digest": GO_DIGEST,
            "executed_at": timestamp(1),
            "checks": {
                "readiness": {"passed": True, "status_code": 200},
                "certificate": {
                    "passed": True,
                    "verified": True,
                    "expires_at": "2027-08-06T00:00:00Z",
                },
                "version": {"passed": True, "actual": "v1.2.3", "expected": "v1.2.3"},
                "static_ui": {"passed": True, "requests": 10, "errors": 0},
                "controller": {"passed": True, "requests": 10, "errors": 0},
                "shadow": {
                    "passed": True,
                    "read_only": True,
                    "requests": 100,
                    "mutation_requests": 0,
                    "unapproved_differences": 0,
                },
            },
        }
        cutover = {
            "schema_version": 1,
            "started_at": timestamp(2),
            "switched_at": timestamp(3),
            "completed_at": timestamp(4),
            "initial_target_digest": SCALA_DIGEST,
            "final_target_digest": GO_DIGEST,
            "new_connections_only": True,
            "side_effect_replay": False,
            "duplicated_mutating_requests": 0,
        }
        samples = []
        for minute in range(3, 14):
            samples.append(
                {
                    "captured_at": timestamp(minute),
                    "target_digest": GO_DIGEST,
                    "http_5xx_rate": 0.001,
                    "login_success_rate": 0.999,
                    "p95_ms": 80,
                    "rss_mib": 100,
                    "cpu_percent": 20,
                    "goroutines": 30,
                    "cache_utilization": 0.4,
                    "controller_error_rate": 0.001,
                    "active_file_jobs": 0,
                    "active_command_jobs": 0,
                }
            )
        observation = {
            "schema_version": 1,
            "started_at": timestamp(3),
            "ended_at": timestamp(13),
            "samples": samples,
            "critical_path_failures": [],
            "security_incidents": [],
            "certificate_errors": 0,
            "fips_mode_errors": 0,
            "controller_amplification": False,
        }
        rollback_drill = {
            "schema_version": 1,
            "candidate_digest": GO_DIGEST,
            "scala_digest": SCALA_DIGEST,
            "trigger_rule_id": "http-5xx",
            "triggered_at": timestamp(20),
            "traffic_restored_at": timestamp(20, 10),
            "scala_ready_at": timestamp(20, 30),
            "completed_at": timestamp(21),
            "recovery_seconds": 30,
            "automatic": True,
            "injected": True,
            "readiness_passed": True,
            "no_data_rollback": True,
            "final_target_digest": SCALA_DIGEST,
            "session_behavior": "relogin-required",
            "notification_ticket": "COMMS-11",
        }
        report_values = {
            "preflight": preflight,
            "cutover": cutover,
            "observation": observation,
            "rollback_drill": rollback_drill,
        }
        reports = {
            name: self.write_json(root / f"{name}.json", report)
            for name, report in report_values.items()
        }
        thresholds = []
        values = {
            "http_5xx_rate": 0.01,
            "login_success_rate": 0.98,
            "p95_ms": 500,
            "rss_mib": 512,
            "cpu_percent": 90,
            "goroutines": 500,
            "cache_utilization": 0.9,
            "controller_error_rate": 0.05,
            "active_file_jobs": 10,
            "active_command_jobs": 4,
        }
        for metric, direction in REQUIRED_THRESHOLDS.items():
            thresholds.append(
                {
                    "id": "http-5xx" if metric == "http_5xx_rate" else metric.replace("_", "-"),
                    "metric": metric,
                    "direction": direction,
                    "value": values[metric],
                    "sustain_seconds": 120,
                    "automatic_rollback": True,
                }
            )
        config = {
            "schema_version": 1,
            "change": {
                "id": "RW-011",
                "environment": "synthetic qualification cluster",
                "ticket": "CHANGE-11",
                "approved_by": "change-owner",
                "approved_at": timestamp(-60),
                "window_start": timestamp(0),
                "window_end": timestamp(60),
            },
            "candidate": {
                "go_image": {
                    "reference": f"registry.example/manager@{GO_DIGEST}",
                    "digest": GO_DIGEST,
                },
                "scala_image": {
                    "reference": f"registry.example/manager-scala@{SCALA_DIGEST}",
                    "digest": SCALA_DIGEST,
                },
                "qualification_report": qualification_spec,
            },
            "backups": backups,
            "rollback_runbook": {
                "path": runbook_path.name,
                "sha256": file_digest(runbook_path),
                "owner": "operations-owner",
                "approval_ticket": "RUNBOOK-11",
            },
            "monitoring": {
                "approved_by": "sre-owner",
                "approval_ticket": "SLO-11",
                "approved_at": timestamp(-30),
                "minimum_observation_seconds": 600,
                "max_sample_interval_seconds": 60,
                "max_recovery_seconds": 120,
                "thresholds": thresholds,
            },
            "reports": reports,
            "approvals": {},
        }
        self.sign(config)
        return config, report_values

    def test_complete_cutover_and_rollback_drill_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, _ = self.complete_config(root)

            result = evaluate(root / "evidence.json", config)

            self.assertTrue(result["passed"], [c for c in result["checks"] if not c["passed"]])
            self.assertEqual(result["detected_threshold_triggers"], [])

    def test_rw010_qualification_and_backup_are_mandatory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, _ = self.complete_config(root)
            qualification_path = root / config["candidate"]["qualification_report"]["path"]
            qualification = json.loads(qualification_path.read_text(encoding="utf-8"))
            qualification["passed"] = False
            config["candidate"]["qualification_report"] = self.write_json(
                qualification_path, qualification
            )
            config["backups"]["configuration"]["restore_tested"] = False
            self.sign(config)

            result = evaluate(root / "evidence.json", config)

            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("RW-010 qualification passed", failed)
            self.assertIn("configuration backup controls", failed)

    def test_shadow_and_cutover_side_effects_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, reports = self.complete_config(root)
            reports["preflight"]["checks"]["shadow"]["mutation_requests"] = 1
            reports["cutover"]["duplicated_mutating_requests"] = 1
            self.update_report(root, config, "preflight", reports["preflight"])
            self.update_report(root, config, "cutover", reports["cutover"])

            result = evaluate(root / "evidence.json", config)

            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("read-only shadow safety", failed)
            self.assertIn("cutover side-effect safety", failed)

    def test_transient_threshold_breach_does_not_trigger_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, reports = self.complete_config(root)
            reports["observation"]["samples"][3]["http_5xx_rate"] = 0.2
            self.update_report(root, config, "observation", reports["observation"])

            result = evaluate(root / "evidence.json", config)

            self.assertTrue(result["passed"], [c for c in result["checks"] if not c["passed"]])
            self.assertEqual(result["detected_threshold_triggers"], [])

    def test_duplicate_threshold_and_out_of_window_sample_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, reports = self.complete_config(root)
            config["monitoring"]["thresholds"][-1]["metric"] = "active_file_jobs"
            reports["observation"]["samples"][0]["captured_at"] = timestamp(2)
            self.update_report(root, config, "observation", reports["observation"])

            result = evaluate(root / "evidence.json", config)

            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("threshold metric set", failed)
            self.assertIn("observation metric samples", failed)

    def test_sustained_threshold_requires_matching_live_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, reports = self.complete_config(root)
            for sample in reports["observation"]["samples"][3:6]:
                sample["http_5xx_rate"] = 0.2
            reports["observation"]["samples"] = reports["observation"]["samples"][:6]
            reports["observation"]["ended_at"] = timestamp(8)
            self.update_report(root, config, "observation", reports["observation"])

            result = evaluate(root / "evidence.json", config)

            self.assertFalse(result["passed"])
            self.assertEqual(
                ["http-5xx"],
                [trigger["rule_id"] for trigger in result["detected_threshold_triggers"]],
            )
            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("cutover observation outcome", failed)
            self.assertIn("live rollback report: exists", failed)

    def test_sustained_threshold_with_automatic_live_rollback_passes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, reports = self.complete_config(root)
            for sample in reports["observation"]["samples"][3:6]:
                sample["http_5xx_rate"] = 0.2
            reports["observation"]["samples"] = reports["observation"]["samples"][:6]
            reports["observation"]["ended_at"] = timestamp(8)
            self.update_report(root, config, "observation", reports["observation"])
            live_rollback = dict(reports["rollback_drill"])
            live_rollback.update(
                {
                    "triggered_at": timestamp(8),
                    "traffic_restored_at": timestamp(8, 10),
                    "scala_ready_at": timestamp(8, 30),
                    "completed_at": timestamp(9),
                    "injected": False,
                }
            )
            self.update_report(root, config, "live_rollback", live_rollback)

            result = evaluate(root / "evidence.json", config)

            self.assertTrue(result["passed"], [c for c in result["checks"] if not c["passed"]])

    def test_stale_approval_and_slow_rollback_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config, reports = self.complete_config(root)
            reports["rollback_drill"]["scala_ready_at"] = timestamp(23)
            reports["rollback_drill"]["completed_at"] = timestamp(24)
            reports["rollback_drill"]["recovery_seconds"] = 180
            self.update_report(root, config, "rollback_drill", reports["rollback_drill"])
            config["approvals"]["release"]["execution_digest"] = "sha256:" + "0" * 64

            result = evaluate(root / "evidence.json", config)

            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("rollback drill: recovery", failed)
            self.assertIn("approval: release", failed)


if __name__ == "__main__":
    unittest.main()
