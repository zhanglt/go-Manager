#!/usr/bin/env python3
"""Deterministically sanitize an approved Controller fixture before it is archived."""

from __future__ import annotations

import argparse
import base64
import hashlib
import hmac
import ipaddress
import json
import os
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit


SENSITIVE_KEY = re.compile(
    r"(^|_)(passwd|secret|cookie|authorization|credential|private_key|session_token)($|_)",
    re.IGNORECASE,
)
IDENTITY_KEYS = {
    "id", "name", "display_name", "fullname", "username", "user_name", "cluster_name",
    "domain", "host_name", "hostname", "service", "service_group", "image", "image_id",
    "enforcer_name", "scanner_name", "registry", "repository", "nickname", "author",
}
FREE_TEXT_KEYS = {"comment", "description", "message", "details", "phone", "address"}
EMAIL_RE = re.compile(r"(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b")
TOKEN_RE = re.compile(r"(?i)\b(?:bearer\s+)?[A-Za-z0-9_+/=-]{32,}\b")
URL_RE = re.compile(r"(?i)https?://[^\s\"']+")


def sensitive_key(key: str) -> bool:
    lowered = key.lower()
    return lowered in {"password", "token", "access_token", "refresh_token", "id_token"} or bool(
        SENSITIVE_KEY.search(key)
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--salt-env", default="RW004_SANITIZE_KEY")
    parser.add_argument("--approval-id", required=True)
    return parser.parse_args()


def pseudonym(value: str, salt: bytes, prefix: str = "fixture") -> str:
    digest = hmac.new(salt, value.encode(), hashlib.sha256).hexdigest()[:12]
    return f"{prefix}-{digest}"


def sanitize_ip(value: str, salt: bytes) -> str:
    address = ipaddress.ip_address(value)
    digest = hmac.new(salt, value.encode(), hashlib.sha256).digest()
    if address.version == 4:
        return f"192.0.2.{1 + digest[0] % 254}"
    return f"2001:db8::{int.from_bytes(digest[:2], 'big'):x}"


def sanitize_string(value: str, key: str, salt: bytes) -> str:
    if sensitive_key(key):
        return "<redacted>"
    if key.lower() == "email" or EMAIL_RE.fullmatch(value):
        return pseudonym(value, salt, "user") + "@example.invalid"
    if key.lower() in FREE_TEXT_KEYS:
        return "<redacted>" if value else value
    if key.lower() == "base64":
        return base64.b64encode(b"sanitized-binary-fixture").decode()
    if key.lower().endswith("sha256"):
        return value
    if key.lower() in IDENTITY_KEYS and value:
        return pseudonym(value, salt)
    try:
        return sanitize_ip(value, salt)
    except ValueError:
        pass
    if URL_RE.fullmatch(value):
        parsed = urlsplit(value)
        return f"{parsed.scheme}://service.example.invalid"
    value = EMAIL_RE.sub(lambda match: pseudonym(match.group(0), salt, "user") + "@example.invalid", value)
    value = TOKEN_RE.sub("<redacted>", value)
    return value


def sanitize(value: Any, salt: bytes, key: str = "") -> Any:
    if isinstance(value, dict):
        return {str(name): sanitize(item, salt, str(name)) for name, item in value.items()}
    if isinstance(value, list):
        return [sanitize(item, salt, key) for item in value]
    if isinstance(value, str):
        return sanitize_string(value, key, salt)
    return value


def unsafe_values(value: Any, path: str = "", key_name: str = "") -> list[str]:
    findings: list[str] = []
    if isinstance(value, dict):
        for key, item in value.items():
            child = f"{path}/{str(key).replace('~', '~0').replace('/', '~1')}"
            if sensitive_key(str(key)) and not isinstance(item, (dict, list)) and item not in (None, "", "<redacted>"):
                findings.append(child)
            findings.extend(unsafe_values(item, child, str(key)))
    elif isinstance(value, list):
        for index, item in enumerate(value):
            findings.extend(unsafe_values(item, f"{path}/{index}", key_name))
    elif isinstance(value, str):
        if EMAIL_RE.search(value) and not value.endswith("@example.invalid"):
            findings.append(path or "/")
        exempt_token_pattern = key_name.lower().endswith("sha256") or key_name.lower() == "base64"
        if TOKEN_RE.search(value) and value != "<redacted>" and not exempt_token_pattern:
            findings.append(path or "/")
        for match in URL_RE.finditer(value):
            if urlsplit(match.group(0)).hostname != "service.example.invalid":
                findings.append(path or "/")
    return sorted(set(findings))


def main() -> int:
    args = parse_args()
    secret = os.environ.get(args.salt_env, "")
    if len(secret) < 32:
        raise SystemExit(f"{args.salt_env} must contain at least 32 characters")
    source = json.loads(args.input.read_text(encoding="utf-8"))
    output = sanitize(source, secret.encode())
    findings = unsafe_values(output)
    if findings:
        raise SystemExit(f"sanitized fixture still contains sensitive values at: {findings[:20]}")
    if not isinstance(output, dict):
        raise SystemExit("fixture root must be an object")
    output["sanitization"] = {
        "schema_version": 1,
        "approval_id": args.approval_id,
        "sanitized_at": datetime.now(timezone.utc).isoformat(),
        "algorithm": "HMAC-SHA256 deterministic pseudonyms; secrets redacted",
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(output, indent=2) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
