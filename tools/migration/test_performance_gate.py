#!/usr/bin/env python3

import unittest

from performance_gate import (
    evaluate,
    evaluate_smoke,
    percentile,
    request_body,
    resolve_headers,
    slope,
    summarize_resources,
)


def scenario(name, rss, throughput=100, p95=10, growth=0, goroutines=0, fds=0):
    return {
        "name": name,
        "process_alive": True,
        "startup_seconds": 1,
        "load": {
            "requests": 100 if throughput else 0,
            "errors": 0,
            "throughput_rps": throughput,
            "latency_ms": {"p50": p95 / 2, "p95": p95, "p99": p95 * 2},
        },
        "resources": {
            "rss_kib": {"min": rss, "median": rss, "max": rss},
            "rss_kib_growth": growth,
            "goroutines_growth": goroutines,
            "fds_growth": fds,
            "threads_growth": 0,
            "processes_growth": 0,
        },
        "samples": [{"rss_kib": rss}],
    }


class PerformanceGateTest(unittest.TestCase):
    def test_smoke_gate_requires_both_implementations_and_clean_scenarios(self):
        report = {
            "implementations": [
                {"name": name, "runs": [{"scenarios": [{"name": "steady", "process_alive": True, "load": {"errors": 0}}]}]}
                for name in ("scala", "go")
            ]
        }
        self.assertTrue(evaluate_smoke(report, ["steady"])["passed"])
        report["implementations"][1]["runs"][0]["scenarios"][0]["load"]["errors"] = 1
        self.assertFalse(evaluate_smoke(report, ["steady"])["passed"])

    def test_percentile_and_slope(self):
        histogram = [0] * 4
        histogram[1:] = [1, 2, 1]
        self.assertEqual(percentile(histogram, 0.5), 2)
        self.assertEqual(slope([(0, 10), (3600, 20), (7200, 30)]), 10)

    def test_generated_bodies_have_exact_size(self):
        self.assertEqual(len(request_body({"body_json_bytes": 1024})), 1024)
        self.assertEqual(len(request_body({"body_multipart_bytes": 1024})), 1024)

    def test_sequence_header_is_bounded(self):
        headers = resolve_headers(
            {"Token": {"sequence": "cache-{sequence}", "modulo": 10}}, sequence=12
        )
        self.assertEqual(headers["Token"], "cache-2")

    def test_resource_summary_includes_child_process_growth(self):
        samples = [
            {
                "timestamp": 0,
                "cpu_seconds": 0,
                "rss_kib": 100,
                "hwm_kib": 100,
                "threads": 2,
                "fds": 3,
                "processes": 1,
                "goroutines": None,
            },
            {
                "timestamp": 60,
                "cpu_seconds": 1,
                "rss_kib": 110,
                "hwm_kib": 110,
                "threads": 2,
                "fds": 3,
                "processes": 2,
                "goroutines": None,
            },
        ]

        summary = summarize_resources(samples)

        self.assertEqual(summary["processes_growth"], 1)
        self.assertEqual(summary["processes_slope_per_hour"], 60)

    def test_gate_accepts_thresholds_and_candidate_only_stability(self):
        report = {
            "criteria": {
                "baseline": "scala",
                "candidate": "go",
                "stability_scenario": "stability-24h",
            },
            "implementations": [
                {"name": "scala", "runs": [{"scenarios": [scenario("steady", 1000)]}]},
                {
                    "name": "go",
                    "runs": [
                        {
                            "scenarios": [
                                scenario("steady", 500, throughput=100, p95=11),
                                scenario("stability-24h", 500, growth=10),
                            ]
                        }
                    ],
                },
            ],
        }
        result = evaluate(report)
        self.assertTrue(result["passed"], result["checks"])

    def test_gate_rejects_rss_regression(self):
        report = {
            "criteria": {"baseline": "scala", "candidate": "go"},
            "implementations": [
                {"name": "scala", "runs": [{"scenarios": [scenario("idle", 1000, 0)]}]},
                {"name": "go", "runs": [{"scenarios": [scenario("idle", 600, 0)]}]},
            ],
        }
        result = evaluate(report)
        self.assertFalse(result["passed"])
        self.assertFalse(next(check for check in result["checks"] if check["name"] == "idle: RSS")["passed"])

    def test_formal_gate_rejects_scaled_or_incomplete_report(self):
        report = {
            "settings": {"duration_scale": 0.01, "resource_limits": {"enforced": False}},
            "criteria": {
                "baseline": "scala",
                "candidate": "go",
                "required_scenarios": ["idle", "stability-24h"],
                "stability_scenario": "stability-24h",
                "minimum_runs": 3,
                "minimum_stability_seconds": 86400,
            },
            "implementations": [
                {"name": "scala", "runs": [{"scenarios": [scenario("idle", 1000, 0)]}]},
                {"name": "go", "runs": [{"scenarios": [scenario("idle", 500, 0)]}]},
            ],
        }
        result = evaluate(report)
        self.assertFalse(result["passed"])
        failed = {check["name"] for check in result["checks"] if not check["passed"]}
        self.assertIn("formal duration scale", failed)
        self.assertIn("resource limits enforced", failed)
        self.assertIn("stability-24h: duration", failed)


if __name__ == "__main__":
    unittest.main()
