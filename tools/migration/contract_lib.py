#!/usr/bin/env python3
"""Shared primitives for Manager black-box contract tests."""

from __future__ import annotations

import base64
import copy
import gzip
import hashlib
import http.client
import json
import os
import re
import ssl
from dataclasses import dataclass
from typing import Any
from urllib.parse import urljoin, urlsplit


ENV_RE = re.compile(r"^\$\{ENV:([A-Za-z_][A-Za-z0-9_]*)}$")
SENSITIVE_HEADERS = {"authorization", "cookie", "set-cookie", "token", "x-auth-token", "x-r-sess"}


@dataclass
class Capture:
    status: int | None
    reason: str | None
    headers: dict[str, list[str]]
    raw_body: bytes
    decoded_body: bytes
    json_body: Any | None
    error: str | None = None

    def summary(self) -> dict[str, Any]:
        headers = {
            name: (["***"] if name in SENSITIVE_HEADERS else values)
            for name, values in sorted(self.headers.items())
        }
        return {
            "status": self.status,
            "reason": self.reason,
            "headers": headers,
            "raw_body_bytes": len(self.raw_body),
            "raw_body_sha256": hashlib.sha256(self.raw_body).hexdigest(),
            "decoded_body_bytes": len(self.decoded_body),
            "decoded_body_sha256": hashlib.sha256(self.decoded_body).hexdigest(),
            "body_type": "json" if self.json_body is not None else "binary",
            "error": self.error,
        }


def resolve_env(value: Any) -> Any:
    """Resolve exact ${ENV:NAME} placeholders without persisting their values."""
    if isinstance(value, str):
        match = ENV_RE.match(value)
        if match:
            name = match.group(1)
            if name not in os.environ:
                raise ValueError(f"required environment variable is not set: {name}")
            return os.environ[name]
        return value
    if isinstance(value, list):
        return [resolve_env(item) for item in value]
    if isinstance(value, dict):
        return {key: resolve_env(item) for key, item in value.items()}
    return value


def request_body(request: dict[str, Any]) -> bytes:
    variants = [name for name in ("body_json", "body_text", "body_base64") if name in request]
    if len(variants) > 1:
        raise ValueError(f"request has multiple body variants: {variants}")
    if "body_json" in request:
        return json.dumps(resolve_env(request["body_json"]), separators=(",", ":")).encode()
    if "body_text" in request:
        return str(resolve_env(request["body_text"])).encode()
    if "body_base64" in request:
        return base64.b64decode(resolve_env(request["body_base64"]), validate=True)
    return b""


def send_request(base_url: str, request: dict[str, Any], timeout: float, insecure: bool) -> Capture:
    request_path = str(request["path"])
    parsed_request_path = urlsplit(request_path)
    if not request_path.startswith("/") or parsed_request_path.scheme or parsed_request_path.netloc:
        raise ValueError(f"contract request path must be an absolute path, not a URL: {request_path}")
    target = urlsplit(urljoin(base_url.rstrip("/") + "/", request_path.lstrip("/")))
    headers = {str(name): str(value) for name, value in resolve_env(request.get("headers", {})).items()}
    body = request_body(request)
    if "body_json" in request and not any(name.lower() == "content-type" for name in headers):
        headers["Content-Type"] = "application/json"

    connection: http.client.HTTPConnection
    if target.scheme == "https":
        context = ssl._create_unverified_context() if insecure else ssl.create_default_context()
        connection = http.client.HTTPSConnection(target.hostname, target.port, timeout=timeout, context=context)
    elif target.scheme == "http":
        connection = http.client.HTTPConnection(target.hostname, target.port, timeout=timeout)
    else:
        raise ValueError(f"unsupported URL scheme: {target.scheme}")

    path = target.path or "/"
    if target.query:
        path += "?" + target.query
    try:
        connection.request(request.get("method", "GET").upper(), path, body=body, headers=headers)
        response = connection.getresponse()
        raw = response.read()
        response_headers: dict[str, list[str]] = {}
        for name, value in response.getheaders():
            response_headers.setdefault(name.lower(), []).append(value)
        decoded = raw
        if any("gzip" in value.lower() for value in response_headers.get("content-encoding", [])):
            decoded = gzip.decompress(raw)
        parsed_json = None
        try:
            parsed_json = json.loads(decoded)
        except (json.JSONDecodeError, UnicodeDecodeError):
            pass
        return Capture(response.status, response.reason, response_headers, raw, decoded, parsed_json)
    except Exception as error:  # The error class is part of the contract result, not a harness crash.
        return Capture(None, None, {}, b"", b"", None, type(error).__name__)
    finally:
        connection.close()


def remove_json_pointer(value: Any, pointer: str) -> None:
    if pointer in ("", "/"):
        return
    if not pointer.startswith("/"):
        raise ValueError(f"JSON ignore path must be an RFC 6901 pointer: {pointer}")
    parts = [part.replace("~1", "/").replace("~0", "~") for part in pointer[1:].split("/")]
    parent = value
    for part in parts[:-1]:
        if isinstance(parent, dict) and part in parent:
            parent = parent[part]
        elif isinstance(parent, list) and part.isdigit() and int(part) < len(parent):
            parent = parent[int(part)]
        else:
            return
    final = parts[-1]
    if isinstance(parent, dict):
        parent.pop(final, None)
    elif isinstance(parent, list) and final.isdigit() and int(final) < len(parent):
        parent[int(final)] = "<ignored>"


def json_difference_paths(left: Any, right: Any, path: str = "") -> list[str]:
    if type(left) is not type(right):
        return [path or "/"]
    if isinstance(left, dict):
        differences: list[str] = []
        for key in sorted(set(left) | set(right)):
            child = path + "/" + str(key).replace("~", "~0").replace("/", "~1")
            if key not in left or key not in right:
                differences.append(child)
            else:
                differences.extend(json_difference_paths(left[key], right[key], child))
        return differences
    if isinstance(left, list):
        differences = []
        if len(left) != len(right):
            differences.append(path + "/length")
        for index, (left_item, right_item) in enumerate(zip(left, right)):
            differences.extend(json_difference_paths(left_item, right_item, f"{path}/{index}"))
        return differences
    return [] if left == right else [path or "/"]


def compare(
    left: Capture,
    right: Capture,
    ignored_headers: set[str] | None = None,
    ignored_json_pointers: list[str] | None = None,
) -> list[dict[str, Any]]:
    ignored_headers = {name.lower() for name in (ignored_headers or set())}
    ignored_json_pointers = ignored_json_pointers or []
    differences: list[dict[str, Any]] = []

    if left.error != right.error:
        differences.append({"kind": "transport_error", "path": "/", "left": left.error, "right": right.error})
        return differences
    if left.status != right.status:
        differences.append({"kind": "status", "path": "/status", "left": left.status, "right": right.status})

    left_headers = {key: value for key, value in left.headers.items() if key not in ignored_headers}
    right_headers = {key: value for key, value in right.headers.items() if key not in ignored_headers}
    for name in sorted(set(left_headers) | set(right_headers)):
        if left_headers.get(name) != right_headers.get(name):
            differences.append({"kind": "header", "path": f"/headers/{name}"})

    if left.json_body is not None and right.json_body is not None:
        left_json = copy.deepcopy(left.json_body)
        right_json = copy.deepcopy(right.json_body)
        for pointer in ignored_json_pointers:
            remove_json_pointer(left_json, pointer)
            remove_json_pointer(right_json, pointer)
        differences.extend(
            {"kind": "json", "path": path}
            for path in json_difference_paths(left_json, right_json)
        )
    elif left.decoded_body != right.decoded_body:
        differences.append({"kind": "body", "path": "/body"})
    return differences
