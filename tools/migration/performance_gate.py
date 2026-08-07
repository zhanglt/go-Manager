#!/usr/bin/env python3
"""Run reproducible Manager performance suites and enforce Scala/Go gates."""

from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import math
import os
import platform
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
import time
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from statistics import median
from typing import Any
from urllib.parse import urlsplit


SCHEMA_VERSION = 1
LATENCY_BUCKET_US = 1_000
LATENCY_BUCKETS = 60_001


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="action", required=True)
    run = subparsers.add_parser("run", help="run all configured implementations and scenarios")
    run.add_argument("--config", type=Path, required=True)
    run.add_argument("--output", type=Path, required=True)
    run.add_argument("--runs", type=int)
    run.add_argument("--scenario", action="append", default=[])
    run.add_argument("--duration-scale", type=float, default=1.0)
    run.add_argument("--keep-logs", type=Path)
    check = subparsers.add_parser("check", help="evaluate a machine-readable run report")
    check.add_argument("--report", type=Path, required=True)
    check.add_argument("--output", type=Path)
    smoke = subparsers.add_parser("smoke-check", help="validate a shortened scheduled smoke report")
    smoke.add_argument("--report", type=Path, required=True)
    smoke.add_argument("--scenario", action="append", required=True)
    smoke.add_argument("--output", type=Path)
    return parser.parse_args()


def percentile(histogram: list[int], quantile: float) -> float:
    total = sum(histogram)
    if total == 0:
        return 0.0
    target = max(1, math.ceil(total * quantile))
    seen = 0
    for index, count in enumerate(histogram):
        seen += count
        if seen >= target:
            return index * LATENCY_BUCKET_US / 1_000
    return (len(histogram) - 1) * LATENCY_BUCKET_US / 1_000


def slope(values: list[tuple[float, float]]) -> float:
    """Return least-squares units per hour for monotonic timestamps."""
    if len(values) < 2 or values[-1][0] == values[0][0]:
        return 0.0
    origin = values[0][0]
    points = [((timestamp - origin) / 3600, value) for timestamp, value in values]
    x_mean = sum(point[0] for point in points) / len(points)
    y_mean = sum(point[1] for point in points) / len(points)
    denominator = sum((point[0] - x_mean) ** 2 for point in points)
    if denominator == 0:
        return 0.0
    return sum((x - x_mean) * (y - y_mean) for x, y in points) / denominator


def render(value: str, variables: dict[str, Any]) -> str:
    return value.format_map({name: str(item) for name, item in variables.items()})


def expanded_env(values: dict[str, Any], variables: dict[str, Any]) -> dict[str, str]:
    environment = os.environ.copy()
    for name, value in values.items():
        environment[name] = render(str(value), variables)
    return environment


def resolve_headers(values: dict[str, Any], sequence: int = 0) -> dict[str, str]:
    result: dict[str, str] = {}
    for name, value in values.items():
        if isinstance(value, dict) and set(value) == {"env"}:
            variable = str(value["env"])
            if variable not in os.environ:
                raise ValueError(f"request header environment variable is unset: {variable}")
            result[name] = os.environ[variable]
        elif isinstance(value, dict) and "sequence" in value:
            modulo = int(value.get("modulo", 0))
            number = sequence % modulo if modulo else sequence
            result[name] = str(value["sequence"]).format(sequence=number)
        else:
            result[name] = str(value)
    return result


def process_tree(root_pid: int) -> list[int]:
    parent_by_pid: dict[int, int] = {}
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            stat = (entry / "stat").read_text(encoding="utf-8")
            fields = stat[stat.rfind(")") + 2 :].split()
            parent_by_pid[int(entry.name)] = int(fields[1])
        except (FileNotFoundError, PermissionError, ValueError, IndexError):
            continue
    result = [root_pid]
    known = {root_pid}
    changed = True
    while changed:
        changed = False
        for pid, parent in parent_by_pid.items():
            if pid not in known and parent in known:
                result.append(pid)
                known.add(pid)
                changed = True
    return result


def read_resources(root_pid: int) -> dict[str, float]:
    totals: Counter[str] = Counter()
    pids = process_tree(root_pid)
    for pid in pids:
        try:
            for line in Path(f"/proc/{pid}/status").read_text(encoding="utf-8").splitlines():
                key, _, raw_value = line.partition(":")
                if key in {"VmRSS", "VmHWM", "Threads"}:
                    totals[key] += int(raw_value.strip().split()[0])
            totals["fds"] += len(list(Path(f"/proc/{pid}/fd").iterdir()))
            stat = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
            fields = stat[stat.rfind(")") + 2 :].split()
            totals["cpu_ticks"] += int(fields[11]) + int(fields[12])
        except (FileNotFoundError, PermissionError, ValueError, IndexError):
            continue
    return {
        "rss_kib": float(totals["VmRSS"]),
        "hwm_kib": float(totals["VmHWM"]),
        "threads": float(totals["Threads"]),
        "fds": float(totals["fds"]),
        "processes": float(len(pids)),
        "cpu_seconds": totals["cpu_ticks"] / os.sysconf("SC_CLK_TCK"),
    }


def scrape_goroutines(url: str | None) -> float | None:
    if not url:
        return None
    parsed = urlsplit(url)
    connection: http.client.HTTPConnection
    if parsed.scheme == "https":
        context = ssl.create_default_context()
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        connection = http.client.HTTPSConnection(parsed.hostname, parsed.port, timeout=2, context=context)
    else:
        connection = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=2)
    try:
        connection.request("GET", parsed.path or "/metrics")
        response = connection.getresponse()
        body = response.read().decode("utf-8", errors="replace")
        for line in body.splitlines():
            if line.startswith("go_goroutines "):
                return float(line.split()[1])
    except OSError:
        return None
    finally:
        connection.close()
    return None


class ManagedProcess:
    def __init__(self, definition: dict[str, Any], variables: dict[str, Any], log_path: Path):
        self.definition = definition
        self.variables = variables.copy()
        command = [render(str(part), variables) for part in definition["command"]]
        cwd = Path(render(str(definition.get("cwd", ".")), variables)).resolve()
        self.log = log_path.open("w", encoding="utf-8")
        self.process = subprocess.Popen(
            command,
            cwd=cwd,
            env=expanded_env(definition.get("env", {}), variables),
            stdout=self.log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
            text=True,
        )

    @property
    def pid(self) -> int:
        pid_file = self.definition.get("pid_file")
        if not pid_file:
            return self.process.pid
        return int(Path(render(str(pid_file), self.variables)).read_text(encoding="utf-8").strip())

    def stop(self) -> None:
        if self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=5)
        self.log.close()


def wait_for_port(process: ManagedProcess, host: str, port: int, timeout: float) -> float:
    started = time.monotonic()
    deadline = started + timeout
    while time.monotonic() < deadline:
        if process.process.poll() is not None:
            raise RuntimeError(f"process exited before listening, status={process.process.returncode}")
        try:
            with socket.create_connection((host, port), timeout=0.2):
                return time.monotonic() - started
        except OSError:
            time.sleep(0.1)
    raise TimeoutError(f"process did not listen on {host}:{port} within {timeout}s")


class RequestStats:
    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.histogram = [0] * LATENCY_BUCKETS
        self.statuses: Counter[str] = Counter()
        self.requests = 0
        self.errors = 0
        self.transport_errors = 0
        self.bytes = 0

    def record(
        self, latency_us: int, status: int | None, size: int, error: bool, transport_error: bool = False
    ) -> None:
        bucket = min(len(self.histogram) - 1, latency_us // LATENCY_BUCKET_US)
        with self.lock:
            self.histogram[bucket] += 1
            self.requests += 1
            self.bytes += size
            self.errors += int(error)
            self.transport_errors += int(transport_error)
            self.statuses[str(status) if status is not None else "transport_error"] += 1

    def report(self, duration: float) -> dict[str, Any]:
        return {
            "requests": self.requests,
            "errors": self.errors,
            "transport_errors": self.transport_errors,
            "status_counts": dict(sorted(self.statuses.items())),
            "response_bytes": self.bytes,
            "throughput_rps": round(self.requests / duration, 3) if duration else 0,
            "latency_ms": {
                "p50": percentile(self.histogram, 0.50),
                "p95": percentile(self.histogram, 0.95),
                "p99": percentile(self.histogram, 0.99),
            },
        }


def request_body(definition: dict[str, Any]) -> bytes | None:
    variants = [
        name
        for name in (
            "body_text",
            "body_json",
            "body_file",
            "body_bytes",
            "body_json_bytes",
            "body_multipart_bytes",
        )
        if name in definition
    ]
    if len(variants) > 1:
        raise ValueError(f"request has multiple body variants: {variants}")
    if "body_text" in definition:
        return str(definition["body_text"]).encode()
    if "body_json" in definition:
        return json.dumps(definition["body_json"], separators=(",", ":")).encode()
    if "body_file" in definition:
        return Path(definition["body_file"]).read_bytes()
    if "body_bytes" in definition:
        return b"x" * int(definition["body_bytes"])
    if "body_json_bytes" in definition:
        size = int(definition["body_json_bytes"])
        prefix, suffix = b'{"padding":"', b'"}'
        if size < len(prefix) + len(suffix):
            raise ValueError("body_json_bytes is too small")
        return prefix + b"x" * (size - len(prefix) - len(suffix)) + suffix
    if "body_multipart_bytes" in definition:
        size = int(definition["body_multipart_bytes"])
        prefix = b'--perf\r\nContent-Disposition: form-data; name="file"; filename="config"\r\n\r\n'
        suffix = b"\r\n--perf--\r\n"
        if size < len(prefix) + len(suffix):
            raise ValueError("body_multipart_bytes is too small")
        return prefix + b"x" * (size - len(prefix) - len(suffix)) + suffix
    return None


def run_load(base_url: str, scenario: dict[str, Any], duration: float) -> dict[str, Any]:
    requests = []
    for definition in scenario.get("requests", []):
        requests.extend([(definition, request_body(definition))] * int(definition.get("weight", 1)))
    if not requests:
        time.sleep(duration)
        return RequestStats().report(duration)
    concurrency = int(scenario.get("concurrency", 1))
    target_rps = float(scenario.get("rps", 0))
    expected_default = {int(value) for value in scenario.get("expected_status", [200])}
    deadline = time.monotonic() + duration
    stats = RequestStats()
    sequence = 0
    sequence_lock = threading.Lock()
    pace_lock = threading.Lock()
    next_request = time.monotonic()
    parsed = urlsplit(base_url)
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE

    def worker() -> None:
        nonlocal sequence, next_request
        connection: http.client.HTTPConnection | None = None
        while time.monotonic() < deadline:
            if target_rps:
                with pace_lock:
                    scheduled = next_request
                    next_request += 1 / target_rps
                delay = scheduled - time.monotonic()
                if delay > 0:
                    time.sleep(delay)
                if time.monotonic() >= deadline:
                    break
            with sequence_lock:
                definition, body = requests[sequence % len(requests)]
                sequence += 1
            if connection is None:
                if parsed.scheme == "https":
                    connection = http.client.HTTPSConnection(
                        parsed.hostname, parsed.port, timeout=float(scenario.get("request_timeout", 60)), context=context
                    )
                else:
                    connection = http.client.HTTPConnection(
                        parsed.hostname, parsed.port, timeout=float(scenario.get("request_timeout", 60))
                    )
            headers = resolve_headers(definition.get("headers", {}), sequence)
            if body is not None:
                headers.setdefault("Content-Length", str(len(body)))
            started = time.perf_counter_ns()
            status: int | None = None
            size = 0
            failed = False
            transport_error = False
            try:
                path = str(definition.get("path", "/"))
                connection.request(str(definition.get("method", "GET")), path, body=body, headers=headers)
                response = connection.getresponse()
                status = response.status
                size = len(response.read())
                expected = {int(value) for value in definition.get("expected_status", expected_default)}
                failed = status not in expected
            except (OSError, http.client.HTTPException):
                transport_error = True
                failed = not bool(scenario.get("allow_transport_errors", False))
                if connection:
                    connection.close()
                connection = None
            stats.record(
                (time.perf_counter_ns() - started) // 1_000,
                status,
                size,
                failed,
                transport_error,
            )
        if connection:
            connection.close()

    threads = [threading.Thread(target=worker, daemon=True) for _ in range(concurrency)]
    started = time.monotonic()
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return stats.report(time.monotonic() - started)


def summarize_resources(samples: list[dict[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for metric in ("rss_kib", "hwm_kib", "threads", "fds", "processes"):
        values = [sample[metric] for sample in samples]
        result[metric] = {"min": min(values), "median": median(values), "max": max(values)}
    if len(samples) > 1:
        elapsed = samples[-1]["timestamp"] - samples[0]["timestamp"]
        cpu_delta = samples[-1]["cpu_seconds"] - samples[0]["cpu_seconds"]
        result["cpu_percent_mean"] = round(100 * cpu_delta / elapsed, 3) if elapsed else 0
    else:
        result["cpu_percent_mean"] = 0
    for metric in ("rss_kib", "threads", "fds", "processes", "goroutines"):
        points = [(sample["timestamp"], sample[metric]) for sample in samples if sample.get(metric) is not None]
        if points:
            result[f"{metric}_slope_per_hour"] = round(slope(points), 3)
            result[f"{metric}_growth"] = round(points[-1][1] - points[0][1], 3)
    return result


def run_scenario(
    process: ManagedProcess,
    implementation: dict[str, Any],
    scenario: dict[str, Any],
    base_url: str,
    duration_scale: float,
) -> dict[str, Any]:
    warmup = float(scenario.get("warmup_seconds", 0)) * duration_scale
    duration = float(scenario["duration_seconds"]) * duration_scale
    if duration <= 0 or warmup < 0:
        raise ValueError("scaled scenario durations must be positive")
    if warmup:
        run_load(base_url, scenario, warmup)
    samples: list[dict[str, Any]] = []
    stop_sampling = threading.Event()
    interval = float(scenario.get("sample_interval_seconds", 1))
    metrics_url = implementation.get("metrics_url")

    def sample() -> None:
        while not stop_sampling.is_set():
            item: dict[str, Any] = {"timestamp": time.time(), **read_resources(process.pid)}
            item["goroutines"] = scrape_goroutines(metrics_url)
            samples.append(item)
            stop_sampling.wait(interval)

    sampler = threading.Thread(target=sample, daemon=True)
    sampler.start()
    load = run_load(base_url, scenario, duration)
    stop_sampling.set()
    sampler.join(timeout=interval + 2)
    if not samples:
        samples.append({"timestamp": time.time(), **read_resources(process.pid), "goroutines": None})
    return {
        "name": scenario["name"],
        "duration_seconds": duration,
        "warmup_seconds": warmup,
        "load": load,
        "gate_throughput": bool(scenario.get("gate_throughput", not scenario.get("rps", 0))),
        "resources": summarize_resources(samples),
        "samples": samples,
        "process_alive": process.process.poll() is None,
    }


def artifact_metadata(implementation: dict[str, Any]) -> dict[str, Any]:
    path_value = implementation.get("artifact")
    if not path_value:
        return {}
    path = Path(path_value).resolve()
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return {"path": str(path), "bytes": path.stat().st_size, "sha256": digest.hexdigest()}


def execute(config: dict[str, Any], args: argparse.Namespace) -> dict[str, Any]:
    if config.get("schema_version") != SCHEMA_VERSION:
        raise ValueError("unsupported or missing config schema_version")
    repetitions = args.runs or int(config.get("runs", 3))
    if repetitions < 3 and not config.get("allow_smoke_runs", False):
        raise ValueError("formal gate requires at least three independent runs")
    selected = set(args.scenario)
    scenarios = [item for item in config["scenarios"] if not selected or item["name"] in selected]
    if selected - {item["name"] for item in scenarios}:
        raise ValueError(f"unknown scenarios: {sorted(selected - {item['name'] for item in scenarios})}")
    port = int(config["server"]["port"])
    host = str(config["server"].get("host", "127.0.0.1"))
    variables = {"port": port, "host": host}
    result: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "host": {
            "hostname": platform.node(),
            "kernel": platform.release(),
            "machine": platform.machine(),
            "cpu_count": os.cpu_count(),
        },
        "settings": {
            "runs": repetitions,
            "port": port,
            "duration_scale": args.duration_scale,
            "resource_limits": config.get("resource_limits", {}),
        },
        "criteria": config.get("criteria", {}),
        "implementations": [],
    }
    log_context = tempfile.TemporaryDirectory(prefix="manager-performance-")
    log_dir = Path(log_context.name)
    controller: ManagedProcess | None = None
    try:
        if config.get("controller"):
            controller = ManagedProcess(config["controller"], variables, log_dir / "controller.log")
            wait_for_port(
                controller,
                str(config["controller"].get("host", "127.0.0.1")),
                int(config["controller"]["port"]),
                float(config["controller"].get("startup_timeout_seconds", 30)),
            )
        for implementation in config["implementations"]:
            implementation_result = {
                "name": implementation["name"],
                "artifact": artifact_metadata(implementation),
                "runs": [],
            }
            for run_number in range(1, repetitions + 1):
                run_result = {"run": run_number, "scenarios": []}
                for scenario in scenarios:
                    allowed_implementations = scenario.get("implementations")
                    if allowed_implementations and implementation["name"] not in allowed_implementations:
                        continue
                    if run_number > int(scenario.get("runs", repetitions)):
                        continue
                    variables.update({"run": run_number, "scenario": scenario["name"]})
                    log_path = log_dir / f"{implementation['name']}-{run_number}-{scenario['name']}.log"
                    manager = ManagedProcess(implementation, variables, log_path)
                    try:
                        startup = wait_for_port(
                            manager,
                            host,
                            port,
                            float(config["server"].get("startup_timeout_seconds", 120)),
                        )
                        base_url = render(str(implementation["base_url"]), variables)
                        scenario_result = run_scenario(
                            manager, implementation, scenario, base_url, args.duration_scale
                        )
                        scenario_result["startup_seconds"] = round(startup, 3)
                        run_result["scenarios"].append(scenario_result)
                    except Exception as error:
                        run_result["scenarios"].append(
                            {"name": scenario["name"], "error": str(error), "process_alive": False}
                        )
                    finally:
                        manager.stop()
                implementation_result["runs"].append(run_result)
            result["implementations"].append(implementation_result)
    finally:
        if controller:
            controller.stop()
        if args.keep_logs:
            args.keep_logs.mkdir(parents=True, exist_ok=True)
            for path in log_dir.iterdir():
                path.replace(args.keep_logs / path.name)
        log_context.cleanup()
    return result


def scenario_rows(report: dict[str, Any], implementation_name: str, scenario_name: str) -> list[dict[str, Any]]:
    implementation = next(
        item for item in report["implementations"] if item["name"] == implementation_name
    )
    return [
        scenario
        for run in implementation["runs"]
        for scenario in run["scenarios"]
        if scenario["name"] == scenario_name
    ]


def evaluate(report: dict[str, Any]) -> dict[str, Any]:
    criteria = report.get("criteria", {})
    baseline = str(criteria.get("baseline", "scala"))
    candidate = str(criteria.get("candidate", "go"))
    max_rss_ratio = float(criteria.get("max_rss_ratio", 0.5))
    min_throughput_ratio = float(criteria.get("min_throughput_ratio", 1.0))
    max_p95_ratio = float(criteria.get("max_p95_ratio", 1.1))
    stability_name = str(criteria.get("stability_scenario", "stability-24h"))
    max_rss_growth_ratio = float(criteria.get("max_stability_rss_growth_ratio", 0.05))
    max_goroutine_growth = float(criteria.get("max_goroutine_growth", 10))
    max_fd_growth = float(criteria.get("max_fd_growth", 5))
    max_thread_growth = float(criteria.get("max_thread_growth", 5))
    max_process_growth = float(criteria.get("max_process_growth", 0))
    scenario_names = sorted(
        {scenario["name"] for item in report["implementations"] for run in item["runs"] for scenario in run["scenarios"]}
    )
    checks: list[dict[str, Any]] = []

    def add(name: str, passed: bool, actual: Any, expected: str) -> None:
        checks.append({"name": name, "passed": passed, "actual": actual, "expected": expected})

    required_scenarios = [str(value) for value in criteria.get("required_scenarios", [])]
    if required_scenarios:
        duration_scale = float(report.get("settings", {}).get("duration_scale", 0))
        add("formal duration scale", duration_scale == 1, duration_scale, "1.0")
        limits_enforced = bool(
            report.get("settings", {}).get("resource_limits", {}).get("enforced", False)
        )
        add("resource limits enforced", limits_enforced, limits_enforced, "true")
        minimum_runs = int(criteria.get("minimum_runs", 3))
        for name in required_scenarios:
            candidate_count = len(scenario_rows(report, candidate, name))
            required_count = 1 if name == stability_name else minimum_runs
            add(
                f"{name}: candidate run count",
                candidate_count >= required_count,
                candidate_count,
                f">= {required_count}",
            )
            if name != stability_name:
                baseline_count = len(scenario_rows(report, baseline, name))
                add(
                    f"{name}: baseline run count",
                    baseline_count >= minimum_runs,
                    baseline_count,
                    f">= {minimum_runs}",
                )
        stability_rows = scenario_rows(report, candidate, stability_name)
        minimum_stability = float(criteria.get("minimum_stability_seconds", 86400))
        actual_stability = max((row.get("duration_seconds", 0) for row in stability_rows), default=0)
        add(
            f"{stability_name}: duration",
            actual_stability >= minimum_stability,
            actual_stability,
            f">= {minimum_stability} seconds",
        )

    for name in scenario_names:
        baseline_rows = scenario_rows(report, baseline, name)
        candidate_rows = scenario_rows(report, candidate, name)
        if name == stability_name and not baseline_rows:
            complete = bool(candidate_rows) and all(
                "error" not in row and row.get("process_alive") for row in candidate_rows
            )
            add(f"{name}: complete and alive", complete, complete, "true")
            if complete:
                errors = sum(row["load"]["errors"] for row in candidate_rows)
                add(f"{name}: request errors", errors == 0, errors, "0")
                for index, row in enumerate(candidate_rows, start=1):
                    add_stability_checks(
                        add,
                        name,
                        index,
                        row,
                        max_rss_growth_ratio,
                        max_goroutine_growth,
                        max_fd_growth,
                        max_thread_growth,
                        max_process_growth,
                    )
            continue
        complete = bool(baseline_rows) and bool(candidate_rows) and all(
            "error" not in row and row.get("process_alive") for row in baseline_rows + candidate_rows
        )
        add(f"{name}: complete and alive", complete, complete, "true")
        if not complete or not baseline_rows or not candidate_rows:
            continue
        baseline_rss = median(row["resources"]["rss_kib"]["max"] for row in baseline_rows)
        candidate_rss = median(row["resources"]["rss_kib"]["max"] for row in candidate_rows)
        rss_ratio = candidate_rss / baseline_rss if baseline_rss else math.inf
        add(f"{name}: RSS", rss_ratio <= max_rss_ratio, round(rss_ratio, 4), f"<= {max_rss_ratio}")
        if any(row["load"]["requests"] for row in baseline_rows):
            baseline_throughput = median(row["load"]["throughput_rps"] for row in baseline_rows)
            candidate_throughput = median(row["load"]["throughput_rps"] for row in candidate_rows)
            throughput_ratio = candidate_throughput / baseline_throughput if baseline_throughput else math.inf
            if all(row.get("gate_throughput", True) for row in baseline_rows + candidate_rows):
                add(
                    f"{name}: throughput",
                    throughput_ratio >= min_throughput_ratio,
                    round(throughput_ratio, 4),
                    f">= {min_throughput_ratio}",
                )
            baseline_p95 = median(row["load"]["latency_ms"]["p95"] for row in baseline_rows)
            candidate_p95 = median(row["load"]["latency_ms"]["p95"] for row in candidate_rows)
            p95_ratio = candidate_p95 / baseline_p95 if baseline_p95 else (1 if candidate_p95 == 0 else math.inf)
            add(f"{name}: P95", p95_ratio <= max_p95_ratio, round(p95_ratio, 4), f"<= {max_p95_ratio}")
            errors = sum(row["load"]["errors"] for row in candidate_rows)
            add(f"{name}: request errors", errors == 0, errors, "0")
        if name == stability_name:
            for index, row in enumerate(candidate_rows, start=1):
                add_stability_checks(
                    add,
                    name,
                    index,
                    row,
                    max_rss_growth_ratio,
                    max_goroutine_growth,
                    max_fd_growth,
                    max_thread_growth,
                    max_process_growth,
                )
    return {
        "schema_version": SCHEMA_VERSION,
        "evaluated_at": datetime.now(timezone.utc).isoformat(),
        "passed": bool(checks) and all(check["passed"] for check in checks),
        "checks": checks,
    }


def evaluate_smoke(report: dict[str, Any], required_scenarios: list[str]) -> dict[str, Any]:
    checks: list[dict[str, Any]] = []
    implementations = report.get("implementations", [])
    criteria = report.get("criteria", {})
    stability_name = str(criteria.get("stability_scenario", "stability-24h"))
    candidate = str(criteria.get("candidate", "go"))
    for implementation in implementations:
        name = str(implementation.get("name", "unknown"))
        rows = [scenario for run in implementation.get("runs", []) for scenario in run.get("scenarios", [])]
        applicable = [scenario for scenario in required_scenarios if scenario != stability_name or name == candidate]
        by_name = {scenario: [row for row in rows if row.get("name") == scenario] for scenario in applicable}
        for scenario, matches in by_name.items():
            complete = bool(matches) and all("error" not in row and row.get("process_alive") for row in matches)
            checks.append(
                {"name": f"{name}/{scenario}: complete and alive", "passed": complete, "actual": complete, "expected": "true"}
            )
            errors = sum(row.get("load", {}).get("errors", 0) for row in matches)
            checks.append(
                {"name": f"{name}/{scenario}: request errors", "passed": errors == 0, "actual": errors, "expected": "0"}
            )
    expected_names = {str(item.get("name")) for item in implementations}
    checks.append(
        {
            "name": "implementations present",
            "passed": expected_names == {"scala", "go"},
            "actual": sorted(expected_names),
            "expected": "scala and go",
        }
    )
    return {
        "schema_version": SCHEMA_VERSION,
        "evaluated_at": datetime.now(timezone.utc).isoformat(),
        "passed": bool(checks) and all(check["passed"] for check in checks),
        "checks": checks,
    }


def add_stability_checks(
    add: Any,
    name: str,
    index: int,
    row: dict[str, Any],
    max_rss_growth_ratio: float,
    max_goroutine_growth: float,
    max_fd_growth: float,
    max_thread_growth: float,
    max_process_growth: float,
) -> None:
    resources = row["resources"]
    initial_rss = row["samples"][0]["rss_kib"]
    allowed_rss = max(initial_rss * max_rss_growth_ratio, 1024)
    add(
        f"{name} run {index}: RSS growth",
        resources["rss_kib_growth"] <= allowed_rss,
        resources["rss_kib_growth"],
        f"<= {round(allowed_rss, 3)} KiB",
    )
    add(
        f"{name} run {index}: goroutine growth",
        resources.get("goroutines_growth", 0) <= max_goroutine_growth,
        resources.get("goroutines_growth"),
        f"<= {max_goroutine_growth}",
    )
    add(
        f"{name} run {index}: FD growth",
        resources["fds_growth"] <= max_fd_growth,
        resources["fds_growth"],
        f"<= {max_fd_growth}",
    )
    add(
        f"{name} run {index}: thread growth",
        resources["threads_growth"] <= max_thread_growth,
        resources["threads_growth"],
        f"<= {max_thread_growth}",
    )
    add(
        f"{name} run {index}: child process growth",
        resources["processes_growth"] <= max_process_growth,
        resources["processes_growth"],
        f"<= {max_process_growth}",
    )


def write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def main() -> int:
    args = parse_args()
    if args.action == "run":
        if args.duration_scale <= 0:
            raise SystemExit("--duration-scale must be positive")
        config = json.loads(args.config.read_text(encoding="utf-8"))
        report = execute(config, args)
        report["gate"] = evaluate(report)
        write_json(args.output, report)
        return 0 if report["gate"]["passed"] else 1
    report = json.loads(args.report.read_text(encoding="utf-8"))
    gate = evaluate_smoke(report, args.scenario) if args.action == "smoke-check" else evaluate(report)
    if args.output:
        write_json(args.output, gate)
    else:
        print(json.dumps(gate, indent=2))
    return 0 if gate["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
