#!/usr/bin/env python3

import copy
import json
import tempfile
import unittest
from pathlib import Path

from release_qualification_gate import (
    REQUIRED_APPROVALS,
    REQUIRED_COVERAGE,
    evaluate,
    file_digest,
    qualification_digest,
)


IMAGE_DIGEST = "sha256:" + "1" * 64
FIPS_DIGEST = "sha256:" + "2" * 64
AMD64_DIGEST = "sha256:" + "3" * 64
ARM64_DIGEST = "sha256:" + "4" * 64


class ReleaseQualificationGateTest(unittest.TestCase):
    def complete_config(self, root: Path) -> dict:
        evidence = []
        for index, (category, coverage) in enumerate(REQUIRED_COVERAGE.items()):
            report = root / f"{category}.json"
            report.write_text(json.dumps({"category": category, "passed": True}), encoding="utf-8")
            evidence.append(
                {
                    "id": f"RW010-{index + 1:02d}",
                    "category": category,
                    "path": report.name,
                    "sha256": file_digest(report),
                    "status": "passed",
                    "image_digests": [FIPS_DIGEST if category == "fips" else IMAGE_DIGEST],
                    "coverage": sorted(coverage),
                    "executed_at": "2026-08-05T02:00:00Z",
                    "environment": "isolated qualification cluster",
                    "approval_ticket": f"QA-{index + 1}",
                }
            )
        config = {
            "schema_version": 1,
            "candidate": {
                "version": "v1.2.3-rc1",
                "commit": "a" * 40,
                "built_at": "2026-08-05T01:00:00Z",
                "image": {
                    "reference": f"registry.example/manager@{IMAGE_DIGEST}",
                    "digest": IMAGE_DIGEST,
                    "platforms": {
                        "linux/amd64": AMD64_DIGEST,
                        "linux/arm64": ARM64_DIGEST,
                    },
                },
                "fips_image": {
                    "reference": f"registry.example/manager-fips@{FIPS_DIGEST}",
                    "digest": FIPS_DIGEST,
                    "platforms": {
                        "linux/amd64": ARM64_DIGEST,
                        "linux/arm64": AMD64_DIGEST,
                    },
                },
            },
            "evidence": evidence,
            "defects": [
                {"id": "BUG-1", "severity": "P1", "status": "verified"},
                {"id": "BUG-2", "severity": "P2", "status": "open"},
            ],
            "accepted_limitations": [
                {
                    "id": "LIMIT-1",
                    "impact": "Users log in again after cutover.",
                    "workaround": "Notify users before the release window.",
                    "owner": "release-owner",
                    "target_version": "v1.3.0",
                    "approval_ticket": "REL-10",
                    "accepted": True,
                    "accepted_at": "2026-08-05",
                }
            ],
            "approvals": {},
        }
        fingerprint = qualification_digest(config)
        config["approvals"] = {
            role: {
                "approved": True,
                "approved_by": f"{role}-owner",
                "approval_ticket": f"G5-{role}",
                "approved_at": "2026-08-05T03:00:00Z",
                "image_digest": IMAGE_DIGEST,
                "fips_image_digest": FIPS_DIGEST,
                "qualification_digest": fingerprint,
            }
            for role in REQUIRED_APPROVALS
        }
        return config

    def test_complete_evidence_passes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = self.complete_config(root)
            result = evaluate(root / "evidence.json", config)
            self.assertTrue(result["passed"], [c for c in result["checks"] if not c["passed"]])

    def test_report_digest_and_candidate_binding_are_enforced(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = self.complete_config(root)
            config["evidence"][0]["sha256"] = "sha256:" + "f" * 64
            config["evidence"][1]["image_digests"] = [FIPS_DIGEST]
            result = evaluate(root / "evidence.json", config)
            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("RW010-01: report digest", failed)
            self.assertIn("RW010-02: candidate binding", failed)

    def test_open_p1_missing_coverage_and_stale_approval_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = self.complete_config(root)
            config["defects"][0]["status"] = "open"
            upgrade = next(item for item in config["evidence"] if item["category"] == "upgrade")
            upgrade["coverage"].remove("session")
            config["approvals"]["release"]["qualification_digest"] = "sha256:" + "0" * 64
            result = evaluate(root / "evidence.json", config)
            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("no unresolved P0/P1 defects", failed)
            self.assertIn("upgrade: required coverage", failed)
            self.assertIn("approval: release", failed)

    def test_incomplete_limitation_fails_and_changes_fingerprint(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = self.complete_config(root)
            original = qualification_digest(config)
            changed = copy.deepcopy(config)
            changed["accepted_limitations"][0]["workaround"] = ""
            self.assertNotEqual(original, qualification_digest(changed))
            result = evaluate(root / "evidence.json", changed)
            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertIn("accepted limitation records", failed)
            self.assertTrue(any(name.startswith("approval:") for name in failed))

    def test_approval_before_latest_evidence_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = self.complete_config(root)
            config["evidence"][0]["executed_at"] = "2026-08-05T03:30:00Z"
            fingerprint = qualification_digest(config)
            for approval in config["approvals"].values():
                approval["qualification_digest"] = fingerprint

            result = evaluate(root / "evidence.json", config)

            failed = {check["name"] for check in result["checks"] if not check["passed"]}
            self.assertEqual(
                {f"approval: {role}" for role in REQUIRED_APPROVALS},
                {name for name in failed if name.startswith("approval:")},
            )


if __name__ == "__main__":
    unittest.main()
