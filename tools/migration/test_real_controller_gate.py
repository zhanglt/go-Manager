#!/usr/bin/env python3

import json
import tempfile
import unittest
from pathlib import Path

from real_controller_gate import evaluate, file_digest


def approval(identifier="approval-one"):
    return {
        "approved": True,
        "approval_id": identifier,
        "approved_by": "qa-owner",
        "approval_ticket": "QA-123",
        "approved_at": "2026-08-05",
    }


class RealControllerGateTest(unittest.TestCase):
    def test_complete_evidence_passes_and_missing_approval_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fixture = root / "fixture.json"
            fixture.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "rules": [],
                        "sanitization": {"schema_version": 1, "approval_id": "approval-one"},
                    }
                ),
                encoding="utf-8",
            )
            (root / "manifest.json").write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "normalization": {
                            "ignored_headers": ["date", "server"],
                            "reason": "non-deterministic transport metadata",
                        },
                        "cases": [],
                    }
                ),
                encoding="utf-8",
            )
            features = {
                name: {"passed": True, "case_ids": [name]}
                for name in ("success", "error", "gzip", "headers", "cookies", "federation", "large_response", "stateful_transaction")
            }
            (root / "contract.json").write_text(
                json.dumps(
                    {
                        "case_count": 304,
                        "failed": 0,
                        "unexplained_difference_count": 0,
                        "unknown_approvals": [],
                        "manifest_sha256": file_digest(root / "manifest.json"),
                        "strict_normalization": True,
                        "features": features,
                    }
                ),
                encoding="utf-8",
            )
            (root / "coverage.json").write_text(
                json.dumps(
                    {
                        "route_count": 263,
                        "covered_route_count": 263,
                        "missing_route_count": 0,
                        "ambiguous_case_count": 0,
                        "unknown_case_count": 0,
                    }
                ),
                encoding="utf-8",
            )
            workflow_ids = ["ui-login", "ui-dashboard", "cli-login", "cli-read"]
            (root / "workflows.json").write_text(
                json.dumps(
                    {
                        "go_backend_verified": True,
                        "workflows": [
                            {"id": item, "status": "passed", "go_backend": True}
                            for item in workflow_ids
                        ],
                    }
                ),
                encoding="utf-8",
            )
            config = {
                "schema_version": 1,
                "approved_environment": approval(),
                "sanitized_fixture": {
                    **approval(),
                    "path": "fixture.json",
                    "sha256": file_digest(fixture),
                },
                "contract": {
                    "manifest": "manifest.json",
                    "report": "contract.json",
                    "coverage_report": "coverage.json",
                    "minimum_cases": 304,
                },
                "required_features": list(features),
                "workflows": {"report": "workflows.json", "required": workflow_ids},
                "qa_approval": approval(),
                "ui_owner_approval": approval(),
            }
            result = evaluate(root / "evidence.json", config)
            self.assertTrue(result["passed"], result["checks"])
            config["qa_approval"]["approved"] = False
            result = evaluate(root / "evidence.json", config)
            self.assertFalse(result["passed"])


if __name__ == "__main__":
    unittest.main()
