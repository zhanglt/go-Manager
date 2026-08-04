#!/usr/bin/env python3

import unittest

from contract_coverage import build_report


class ContractCoverageTest(unittest.TestCase):
    def test_unique_and_explicit_duplicate_coverage(self):
        actions = [
            {"route_id": "sample:GET:/one", "domain": "sample", "method": "GET", "path": "/one", "source": "A.scala", "line": 1},
            {"route_id": "sample:GET:/two#1", "domain": "sample", "method": "GET", "path": "/two", "source": "A.scala", "line": 2},
            {"route_id": "sample:GET:/two#2", "domain": "sample", "method": "GET", "path": "/two", "source": "A.scala", "line": 3},
        ]
        manifest = {
            "schema_version": 1,
            "cases": [
                {"id": "one", "request": {"method": "GET", "path": "/one?value=1"}},
                {"id": "two-first", "covers": ["sample:GET:/two#1"], "request": {"method": "GET", "path": "/two?name=x"}},
            ],
        }
        report = build_report({"actions": actions}, [("manifest.json", manifest)])
        self.assertEqual(report["covered_route_count"], 2)
        self.assertEqual(report["missing_route_count"], 1)
        self.assertEqual(report["ambiguous_case_count"], 0)

    def test_ambiguous_and_unknown_cases_are_reported(self):
        actions = [
            {"route_id": "sample:GET:/two#1", "domain": "sample", "method": "GET", "path": "/two", "source": "A.scala", "line": 2},
            {"route_id": "sample:GET:/two#2", "domain": "sample", "method": "GET", "path": "/two", "source": "A.scala", "line": 3},
        ]
        manifest = {
            "schema_version": 1,
            "cases": [
                {"id": "ambiguous", "request": {"method": "GET", "path": "/two"}},
                {"id": "unknown", "request": {"method": "POST", "path": "/missing"}},
            ],
        }
        report = build_report({"actions": actions}, [("manifest.json", manifest)])
        self.assertEqual(report["ambiguous_case_count"], 1)
        self.assertEqual(report["unknown_case_count"], 1)
        self.assertEqual(report["covered_route_count"], 0)

    def test_any_route_is_covered_by_concrete_request_method(self):
        actions = [
            {"route_id": "sample:ANY:/scanned", "domain": "sample", "method": "ANY", "path": "/scanned", "source": "A.scala", "line": 1}
        ]
        manifest = {
            "schema_version": 1,
            "cases": [{"id": "scanned", "request": {"method": "GET", "path": "/scanned?start=0"}}],
        }
        report = build_report({"actions": actions}, [("manifest.json", manifest)])
        self.assertEqual(report["covered_route_count"], 1)
        self.assertEqual(report["unknown_case_count"], 0)


if __name__ == "__main__":
    unittest.main()
