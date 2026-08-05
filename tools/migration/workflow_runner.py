#!/usr/bin/env python3
"""Run QA-owned UI/CLI workflows without persisting credentials or command output."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import signal
import ssl
import subprocess
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.request import Request, urlopen

from contract_lib import resolve_env


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    return parser.parse_args()


def digest_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def verify_go_backend(probe: dict[str, Any]) -> tuple[bool, dict[str, Any]]:
    url = str(resolve_env(probe["url"]))
    marker = str(probe.get("contains", "go_goroutines"))
    context = ssl._create_unverified_context() if probe.get("insecure") else None
    request = Request(url, headers={"Accept": "text/plain"})
    try:
        with urlopen(request, timeout=float(probe.get("timeout", 10)), context=context) as response:
            body = response.read(int(probe.get("max_bytes", 2_000_000)))
            passed = response.status == 200 and marker.encode() in body
            return passed, {
                "status": response.status,
                "body_bytes": len(body),
                "body_sha256": digest_bytes(body),
                "marker_present": marker.encode() in body,
            }
    except Exception as error:
        return False, {"error": type(error).__name__}


def resolve_workflow_path(config_path: Path, value: str) -> Path:
    path = Path(value)
    return path if path.is_absolute() else (config_path.parent / path).resolve()


def run_workflow(config_path: Path, definition: dict[str, Any], backend_verified: bool) -> dict[str, Any]:
    command = [str(value) for value in resolve_env(definition["command"])]
    environment = os.environ.copy()
    environment.update({str(name): str(value) for name, value in resolve_env(definition.get("env", {})).items()})
    stdin = None
    if definition.get("stdin_env"):
        variable = str(definition["stdin_env"])
        if variable not in os.environ:
            raise ValueError(f"workflow stdin environment variable is unset: {variable}")
        stdin = os.environ[variable].encode()
    cwd = resolve_workflow_path(config_path, str(definition.get("cwd", ".")))
    started = time.monotonic()
    timed_out = False
    with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        process = subprocess.Popen(
            command,
            cwd=cwd,
            env=environment,
            stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL,
            stdout=stdout,
            stderr=stderr,
            start_new_session=True,
        )
        try:
            process.communicate(input=stdin, timeout=float(definition.get("timeout_seconds", 600)))
        except subprocess.TimeoutExpired:
            timed_out = True
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
        stdout.seek(0)
        stderr.seek(0)
        stdout_value = stdout.read()
        stderr_value = stderr.read()
    evidence = []
    evidence_ok = True
    for value in definition.get("evidence_files", []):
        path = resolve_workflow_path(config_path, str(value))
        present = path.is_file()
        evidence_ok = evidence_ok and present
        evidence.append(
            {
                "name": path.name,
                "present": present,
                "bytes": path.stat().st_size if present else 0,
                "sha256": digest_bytes(path.read_bytes()) if present else None,
            }
        )
    passed = process.returncode == 0 and not timed_out and evidence_ok and backend_verified
    return {
        "id": str(definition["id"]),
        "kind": str(definition["kind"]),
        "status": "passed" if passed else "failed",
        "go_backend": backend_verified,
        "duration_seconds": round(time.monotonic() - started, 3),
        "command": {"executable": Path(command[0]).name, "argument_count": len(command) - 1},
        "exit_code": process.returncode,
        "timed_out": timed_out,
        "stdout": {"bytes": len(stdout_value), "sha256": digest_bytes(stdout_value)},
        "stderr": {"bytes": len(stderr_value), "sha256": digest_bytes(stderr_value)},
        "evidence": evidence,
    }


def execute(config_path: Path, config: dict[str, Any]) -> dict[str, Any]:
    if config.get("schema_version") != 1:
        raise ValueError("unsupported or missing workflow config schema_version")
    backend_verified, probe = verify_go_backend(config["backend_probe"])
    workflows = [run_workflow(config_path, item, backend_verified) for item in config.get("workflows", [])]
    return {
        "schema_version": 1,
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "go_backend_verified": backend_verified,
        "backend_probe": probe,
        "workflows": workflows,
        "passed": backend_verified and bool(workflows) and all(item["status"] == "passed" for item in workflows),
    }


def main() -> int:
    args = parse_args()
    config_path = args.config.resolve()
    result = execute(config_path, json.loads(config_path.read_text(encoding="utf-8")))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
