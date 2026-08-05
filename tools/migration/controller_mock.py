#!/usr/bin/env python3
"""Fixture-driven HTTP/HTTPS mock for NeuVector Controller contract tests."""

from __future__ import annotations

import argparse
import base64
import gzip
import hashlib
import json
import socket
import ssl
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, urlsplit


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixtures", type=Path, required=True)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=19443)
    parser.add_argument("--https", action="store_true")
    parser.add_argument("--cert", type=Path)
    parser.add_argument("--key", type=Path)
    return parser.parse_args()


def response_body(response: dict[str, Any]) -> bytes:
    variants = [name for name in ("json", "text", "base64", "repeat") if name in response]
    if len(variants) > 1:
        raise ValueError(f"mock response has multiple body variants: {variants}")
    if "json" in response:
        return json.dumps(response["json"], separators=(",", ":")).encode()
    if "text" in response:
        return str(response["text"]).encode()
    if "base64" in response:
        return base64.b64decode(response["base64"], validate=True)
    if "repeat" in response:
        repeat = response["repeat"]
        value = str(repeat.get("text", "x")).encode()
        count = int(repeat["bytes"])
        if not value or count < 0:
            raise ValueError("mock repeat body requires non-empty text and non-negative bytes")
        return (value * (count // len(value) + 1))[:count]
    return b""


def rule_matches(rule: dict[str, Any], method: str, target: str, headers: Any, body: bytes) -> bool:
    match = rule.get("match", {})
    parsed = urlsplit(target)
    if match.get("method", "GET").upper() != method:
        return False
    if match.get("path", "/") != parsed.path:
        return False
    expected_query = {
        name: [str(value) for value in (values if isinstance(values, list) else [values])]
        for name, values in match.get("query", {}).items()
    }
    if expected_query and expected_query != parse_qs(parsed.query, keep_blank_values=True):
        return False
    for name, value in match.get("headers", {}).items():
        if headers.get(name) != str(value):
            return False
    if "body_sha256" in match and hashlib.sha256(body).hexdigest() != match["body_sha256"]:
        return False
    return True


def read_request_body(handler: BaseHTTPRequestHandler, maximum: int) -> bytes | None:
    transfer_encoding = handler.headers.get("Transfer-Encoding", "").lower()
    if transfer_encoding == "chunked":
        body = bytearray()
        while True:
            line = handler.rfile.readline(8192)
            if not line:
                raise ConnectionError("unexpected EOF in chunk header")
            size = int(line.split(b";", 1)[0].strip(), 16)
            if size == 0:
                while handler.rfile.readline(8192).strip():
                    pass
                return bytes(body)
            if len(body) + size > maximum:
                return None
            chunk = handler.rfile.read(size)
            if len(chunk) != size or handler.rfile.read(2) != b"\r\n":
                raise ConnectionError("invalid chunked request body")
            body.extend(chunk)
    length = int(handler.headers.get("Content-Length", "0"))
    if length > maximum:
        return None
    return handler.rfile.read(length) if length else b""


def build_handler(fixtures: dict[str, Any]) -> type[BaseHTTPRequestHandler]:
    rules = fixtures.get("rules", [])
    response_counts: dict[str, int] = {}
    response_lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def handle_request(self) -> None:
            try:
                body = read_request_body(self, int(fixtures.get("max_request_bytes", 52_428_800)))
            except (ConnectionError, ValueError):
                self.send_error(400)
                return
            if body is None:
                self.send_error(413)
                return
            rule = next(
                (
                    candidate
                    for candidate in rules
                    if rule_matches(candidate, self.command, self.path, self.headers, body)
                ),
                None,
            )
            if rule is None:
                payload = b'{"error":"no matching fixture"}'
                self.send_response(404)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)
                return

            responses = rule.get("responses")
            if responses:
                rule_id = str(rule.get("id", rules.index(rule)))
                with response_lock:
                    response_index = response_counts.get(rule_id, 0)
                    response_counts[rule_id] = response_index + 1
                response = responses[response_index % len(responses)]
            else:
                response = rule.get("response", {})
            if response.get("delay_ms", 0):
                time.sleep(float(response["delay_ms"]) / 1000)
            if response.get("close_connection"):
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return

            payload = response_body(response)
            headers = {str(name): str(value) for name, value in response.get("headers", {}).items()}
            if "json" in response and not any(name.lower() == "content-type" for name in headers):
                headers["Content-Type"] = "application/json"
            if response.get("gzip"):
                payload = gzip.compress(payload, mtime=0)
                headers["Content-Encoding"] = "gzip"

            self.send_response(int(response.get("status", 200)), response.get("reason"))
            for name, value in headers.items():
                self.send_header(name, value)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(payload)

        do_GET = handle_request
        do_POST = handle_request
        do_PUT = handle_request
        do_PATCH = handle_request
        do_DELETE = handle_request
        do_HEAD = handle_request

        def log_message(self, format_string: str, *args: Any) -> None:
            return

    return Handler


def generate_certificate(directory: Path) -> tuple[Path, Path]:
    certificate = directory / "controller-mock.crt"
    key = directory / "controller-mock.key"
    subprocess.run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-keyout",
            str(key),
            "-out",
            str(certificate),
            "-days",
            "1",
            "-subj",
            "/CN=127.0.0.1",
            "-addext",
            "subjectAltName=IP:127.0.0.1,DNS:localhost",
        ],
        check=True,
        capture_output=True,
        text=True,
        timeout=30,
    )
    return certificate, key


def serve(args: argparse.Namespace) -> None:
    fixtures = json.loads(args.fixtures.read_text(encoding="utf-8"))
    if fixtures.get("schema_version") != 1:
        raise SystemExit("unsupported or missing fixture schema_version")
    server = ThreadingHTTPServer((args.host, args.port), build_handler(fixtures))

    with tempfile.TemporaryDirectory() as directory:
        if args.https:
            if bool(args.cert) != bool(args.key):
                raise SystemExit("--cert and --key must be provided together")
            certificate, key = (args.cert, args.key) if args.cert else generate_certificate(Path(directory))
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(certificate, key)
            server.socket = context.wrap_socket(server.socket, server_side=True)
        scheme = "https" if args.https else "http"
        print(f"Controller mock listening on {scheme}://{args.host}:{args.port}", flush=True)
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass
        finally:
            server.server_close()


if __name__ == "__main__":
    serve(parse_args())
