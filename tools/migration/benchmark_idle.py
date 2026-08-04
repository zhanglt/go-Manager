#!/usr/bin/env python3
"""Measure Scala Manager startup time and idle JVM resources using /proc."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import socket
import subprocess
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path
from statistics import mean
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--jar", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--java", default="java")
    parser.add_argument("--port", type=int, default=18443)
    parser.add_argument("--startup-timeout", type=float, default=120)
    parser.add_argument("--warmup", type=float, default=10)
    parser.add_argument("--samples", type=int, default=10)
    parser.add_argument("--interval", type=float, default=1)
    return parser.parse_args()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def java_version(command: str) -> str:
    result = subprocess.run(
        [command, "-version"],
        capture_output=True,
        text=True,
        check=False,
        timeout=10,
    )
    return (result.stderr or result.stdout).splitlines()[0]


def read_status(pid: int) -> dict[str, int]:
    wanted = {"VmRSS", "VmHWM", "VmSize", "Threads"}
    result: dict[str, int] = {}
    for line in Path(f"/proc/{pid}/status").read_text().splitlines():
        key, _, value = line.partition(":")
        if key in wanted:
            result[key] = int(value.strip().split()[0])
    result["FileDescriptors"] = len(list(Path(f"/proc/{pid}/fd").iterdir()))
    stat = Path(f"/proc/{pid}/stat").read_text()
    fields = stat[stat.rfind(")") + 2 :].split()
    result["CpuTicks"] = int(fields[11]) + int(fields[12])
    return result


def wait_for_server(process: subprocess.Popen[Any], port: int, timeout: float) -> float:
    started = time.monotonic()
    deadline = started + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"manager exited before listening, status={process.returncode}")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                return time.monotonic() - started
        except OSError:
            time.sleep(0.1)
    raise TimeoutError(f"manager did not listen on 127.0.0.1:{port} within {timeout}s")


def aggregate(samples: list[dict[str, int]]) -> dict[str, dict[str, float]]:
    metrics = samples[0].keys()
    return {
        metric: {
            "min": min(sample[metric] for sample in samples),
            "mean": round(mean(sample[metric] for sample in samples), 2),
            "max": max(sample[metric] for sample in samples),
        }
        for metric in metrics
    }


def main() -> int:
    args = parse_args()
    jar = args.jar.resolve()
    if not jar.is_file():
        raise SystemExit(f"JAR does not exist: {jar}")
    if args.samples < 1 or args.interval <= 0 or args.warmup < 0:
        raise SystemExit("samples and interval must be positive; warmup must not be negative")

    command = [
        args.java,
        "-Xms256m",
        "-Xmx2048m",
        "-Djdk.tls.rejectClientInitiatedRenegotiation=true",
        "--add-opens=java.base/java.lang=ALL-UNNAMED",
        "--add-opens=java.base/java.lang.reflect=ALL-UNNAMED",
        "--add-opens=java.base/java.util=ALL-UNNAMED",
        "-jar",
        str(jar),
    ]
    environment = os.environ.copy()
    environment.update(
        {
            "MANAGER_SSL": "off",
            "MANAGER_SERVER_PORT": str(args.port),
        }
    )

    with tempfile.TemporaryFile(mode="w+", encoding="utf-8") as log:
        process = subprocess.Popen(
            command,
            cwd=Path.cwd(),
            env=environment,
            stdout=log,
            stderr=subprocess.STDOUT,
            text=True,
        )
        try:
            startup_seconds = wait_for_server(process, args.port, args.startup_timeout)
            time.sleep(args.warmup)
            samples: list[dict[str, int]] = []
            for _ in range(args.samples):
                samples.append(read_status(process.pid))
                time.sleep(args.interval)
            result = {
                "schema_version": 1,
                "captured_at": datetime.now(timezone.utc).isoformat(),
                "scenario": "scala-idle-http",
                "limitations": [
                    "Local workstation measurement; not a release performance result.",
                    "HTTP mode without Controller traffic.",
                    "Uses the locally installed Java runtime, which may differ from the production Java 17 image.",
                ],
                "host": {
                    "kernel": platform.release(),
                    "machine": platform.machine(),
                    "cpu_count": os.cpu_count(),
                },
                "runtime": {
                    "java": java_version(args.java),
                    "heap_min_mb": 256,
                    "heap_max_mb": 2048,
                    "ssl": False,
                    "port": args.port,
                },
                "artifact": {
                    "path": str(args.jar),
                    "bytes": jar.stat().st_size,
                    "sha256": sha256(jar),
                },
                "measurement": {
                    "startup_seconds": round(startup_seconds, 3),
                    "warmup_seconds": args.warmup,
                    "sample_count": args.samples,
                    "sample_interval_seconds": args.interval,
                    "metrics": aggregate(samples),
                },
            }
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
        except Exception as error:
            log.seek(0)
            tail = log.read()[-8000:]
            raise RuntimeError(f"benchmark failed: {error}\n--- manager log tail ---\n{tail}") from error
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
