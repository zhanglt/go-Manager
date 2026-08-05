#!/usr/bin/env python3

import sys
import tempfile
import unittest
from pathlib import Path

from workflow_runner import run_workflow


class WorkflowRunnerTest(unittest.TestCase):
    def test_records_hashes_without_persisting_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            evidence = root / "screen.png"
            evidence.write_bytes(b"fake screenshot")
            result = run_workflow(
                root / "workflow.json",
                {
                    "id": "ui-login",
                    "kind": "ui",
                    "command": [sys.executable, "-c", "print('credential-like output')"],
                    "cwd": ".",
                    "evidence_files": ["screen.png"],
                },
                True,
            )
            self.assertEqual(result["status"], "passed")
            self.assertNotIn("credential-like", str(result))
            self.assertTrue(result["evidence"][0]["present"])

    def test_backend_failure_blocks_workflow(self):
        with tempfile.TemporaryDirectory() as directory:
            result = run_workflow(
                Path(directory) / "workflow.json",
                {
                    "id": "cli-read",
                    "kind": "cli",
                    "command": [sys.executable, "-c", "raise SystemExit(0)"],
                },
                False,
            )
            self.assertEqual(result["status"], "failed")
            self.assertFalse(result["go_backend"])


if __name__ == "__main__":
    unittest.main()
