#!/usr/bin/env python3
"""Run a reproducible Scala Manager contract smoke against the fixture Controller."""

from __future__ import annotations

import argparse
import os
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--jar", type=Path, required=True)
    parser.add_argument("--fixtures", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--manager-port", type=int, default=18443)
    parser.add_argument("--mock-port", type=int, default=19443)
    parser.add_argument("--startup-timeout", type=float, default=120)
    return parser.parse_args()


def wait_port(process: subprocess.Popen[Any], port: int, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"process exited before port {port} was ready, status={process.returncode}")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                return
        except OSError:
            time.sleep(0.1)
    raise TimeoutError(f"port {port} was not ready within {timeout}s")


def ensure_port_available(port: int) -> None:
    with socket.socket() as probe:
        try:
            probe.bind(("127.0.0.1", port))
        except OSError as error:
            raise RuntimeError(f"required smoke port is already in use: {port}") from error


def stop(process: subprocess.Popen[Any] | None) -> None:
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def log_tail(stream: Any) -> str:
    stream.seek(0)
    return stream.read()[-6000:]


def main() -> int:
    args = parse_args()
    for path in (args.jar, args.fixtures, args.manifest):
        if not path.is_file():
            raise SystemExit(f"required input does not exist: {path}")
    ensure_port_available(args.manager_port)
    ensure_port_available(args.mock_port)

    root = Path.cwd()
    tools = Path(__file__).resolve().parent
    mock: subprocess.Popen[Any] | None = None
    manager: subprocess.Popen[Any] | None = None
    with tempfile.TemporaryFile(mode="w+", encoding="utf-8") as mock_log, tempfile.TemporaryFile(
        mode="w+", encoding="utf-8"
    ) as manager_log:
        try:
            mock = subprocess.Popen(
                [
                    sys.executable,
                    str(tools / "controller_mock.py"),
                    "--fixtures",
                    str(args.fixtures),
                    "--https",
                    "--port",
                    str(args.mock_port),
                ],
                cwd=root,
                stdout=mock_log,
                stderr=subprocess.STDOUT,
                text=True,
            )
            wait_port(mock, args.mock_port, 30)

            environment = os.environ.copy()
            environment.update(
                {
                    "MANAGER_SSL": "off",
                    "MANAGER_SERVER_PORT": str(args.manager_port),
                    "CTRL_SERVER_IP": "127.0.0.1",
                    "CTRL_SERVER_PORT": str(args.mock_port),
                    "MANAGER_TEST_TOKEN": "fixture-token-1234567890",
                }
            )
            manager = subprocess.Popen(
                [
                    "java",
                    "-Xms256m",
                    "-Xmx2048m",
                    "-Djdk.tls.rejectClientInitiatedRenegotiation=true",
                    "--add-opens=java.base/java.lang=ALL-UNNAMED",
                    "--add-opens=java.base/java.lang.reflect=ALL-UNNAMED",
                    "--add-opens=java.base/java.util=ALL-UNNAMED",
                    "-jar",
                    str(args.jar),
                ],
                cwd=root,
                env=environment,
                stdout=manager_log,
                stderr=subprocess.STDOUT,
                text=True,
            )
            wait_port(manager, args.manager_port, args.startup_timeout)
            base_url = f"http://127.0.0.1:{args.manager_port}"
            result = subprocess.run(
                [
                    sys.executable,
                    str(tools / "contract_runner.py"),
                    "--manifest",
                    str(args.manifest),
                    "--left-url",
                    base_url,
                    "--right-url",
                    base_url,
                    "--output",
                    str(args.output),
                ],
                cwd=root,
                env=environment,
                check=False,
                text=True,
                timeout=300,
            )
            if result.returncode:
                raise RuntimeError(f"contract runner failed with status {result.returncode}")
        except Exception as error:
            raise RuntimeError(
                f"Scala mock smoke failed: {error}\n"
                f"--- controller mock log ---\n{log_tail(mock_log)}\n"
                f"--- Scala manager log ---\n{log_tail(manager_log)}"
            ) from error
        finally:
            stop(manager)
            stop(mock)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
