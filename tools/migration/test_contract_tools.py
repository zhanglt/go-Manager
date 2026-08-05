#!/usr/bin/env python3

import json
import http.client
import os
import ssl
import sys
import tempfile
import threading
import unittest
from http.server import ThreadingHTTPServer
from pathlib import Path

import contract_runner
from contract_lib import Capture, compare, resolve_env, send_request
from controller_mock import build_handler, generate_certificate, response_body


def capture(body, headers=None):
    encoded = json.dumps(body).encode()
    return Capture(200, "OK", headers or {"content-type": ["application/json"]}, encoded, encoded, body)


class ContractLibraryTest(unittest.TestCase):
    def test_strict_normalization_rejects_broad_or_unexplained_ignores(self):
        with self.assertRaisesRegex(ValueError, "functional headers"):
            contract_runner.validate_normalization(
                {"normalization": {"ignored_headers": ["content-type"], "reason": "bad"}}
            )
        with self.assertRaisesRegex(ValueError, "broad JSON"):
            contract_runner.validate_normalization(
                {"normalization": {"ignored_json_pointers": ["/"], "reason": "bad"}}
            )
        with self.assertRaisesRegex(ValueError, "missing a reason"):
            contract_runner.validate_normalization(
                {"normalization": {"ignored_headers": ["date"]}}
            )

    def test_difference_approvals_must_be_exact_and_owned(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "approvals.json"
            path.write_text(
                json.dumps(
                    {
                        "schema_version": 1,
                        "approved_by": "qa-owner",
                        "approval_ticket": "QA-1",
                        "approved_at": "2026-08-05",
                        "differences": [
                            {
                                "case_id": "case-one",
                                "kind": "header",
                                "path": "/headers/x-runtime",
                                "reason": "implementation identifier",
                            }
                        ],
                    }
                ),
                encoding="utf-8",
            )
            approvals, metadata = contract_runner.load_approvals(path)
            self.assertIn(("case-one", "header", "/headers/x-runtime"), approvals)
            self.assertEqual(metadata["approved_by"], "qa-owner")
            document = json.loads(path.read_text(encoding="utf-8"))
            document["differences"][0]["path"] = "/headers/*"
            path.write_text(json.dumps(document), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "unique and exact"):
                contract_runner.load_approvals(path)
            document["differences"][0].update({"kind": "body", "path": "/body"})
            path.write_text(json.dumps(document), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "requires exact left/right SHA-256"):
                contract_runner.load_approvals(path)

    def test_fixture_repeat_body_and_response_sequence(self):
        self.assertEqual(response_body({"repeat": {"text": "ab", "bytes": 5}}), b"ababa")
        fixtures = {
            "rules": [
                {
                    "id": "sequence",
                    "match": {"method": "GET", "path": "/status"},
                    "responses": [
                        {"status": 200, "text": "up"},
                        {"status": 503, "text": "down"},
                    ],
                }
            ]
        }
        server = ThreadingHTTPServer(("127.0.0.1", 0), build_handler(fixtures))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            first = send_request(
                f"http://127.0.0.1:{server.server_port}",
                {"method": "GET", "path": "/status"},
                timeout=2,
                insecure=False,
            )
            second = send_request(
                f"http://127.0.0.1:{server.server_port}",
                {"method": "GET", "path": "/status"},
                timeout=2,
                insecure=False,
            )
            self.assertEqual((first.status, second.status), (200, 503))
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_fixture_reads_chunked_request_before_responding(self):
        fixtures = {
            "max_request_bytes": 16,
            "rules": [
                {
                    "match": {
                        "method": "POST",
                        "path": "/upload",
                        "body_sha256": "bef57ec7f53a6d40beb640a780a639c83bc29ac8a9816f1fc6c5c6dcd93c4721",
                    },
                    "response": {"status": 200, "text": "ok"},
                }
            ],
        }
        server = ThreadingHTTPServer(("127.0.0.1", 0), build_handler(fixtures))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            connection = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=2)
            connection.request("POST", "/upload", body=iter([b"abc", b"def"]), encode_chunked=True)
            response = connection.getresponse()
            self.assertEqual((response.status, response.read()), (200, b"ok"))
            connection.close()
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_json_pointer_ignore_and_difference_path(self):
        left = capture({"token": {"value": "left"}, "items": [1, 2]})
        right = capture({"token": {"value": "right"}, "items": [1, 3]})
        differences = compare(left, right, ignored_json_pointers=["/token/value"])
        self.assertEqual(differences, [{"kind": "json", "path": "/items/1"}])

    def test_ignored_header_and_sensitive_summary(self):
        left = capture({}, {"date": ["left"], "set-cookie": ["secret=left"]})
        right = capture({}, {"date": ["right"], "set-cookie": ["secret=right"]})
        differences = compare(left, right, ignored_headers={"date"})
        self.assertEqual(differences, [{"kind": "header", "path": "/headers/set-cookie"}])
        self.assertEqual(left.summary()["headers"]["set-cookie"], ["***"])

    def test_environment_placeholder(self):
        os.environ["CONTRACT_TEST_VALUE"] = "resolved"
        try:
            self.assertEqual(resolve_env({"Token": "${ENV:CONTRACT_TEST_VALUE}"}), {"Token": "resolved"})
        finally:
            del os.environ["CONTRACT_TEST_VALUE"]

    def test_request_rejects_absolute_url_override(self):
        with self.assertRaisesRegex(ValueError, "absolute path"):
            send_request(
                "http://127.0.0.1:1",
                {"method": "GET", "path": "https://example.invalid/escape"},
                timeout=1,
                insecure=False,
            )

    def test_fixture_mock_gzip_response(self):
        fixtures = {
            "rules": [
                {
                    "match": {"method": "GET", "path": "/v1/status", "query": {"id": ["1"]}},
                    "response": {"status": 200, "gzip": True, "json": {"status": "ok"}},
                }
            ]
        }
        server = ThreadingHTTPServer(("127.0.0.1", 0), build_handler(fixtures))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            response = send_request(
                f"http://127.0.0.1:{server.server_port}",
                {"method": "GET", "path": "/v1/status?id=1"},
                timeout=2,
                insecure=False,
            )
            self.assertEqual(response.status, 200)
            self.assertEqual(response.json_body, {"status": "ok"})
            self.assertIn("gzip", response.headers["content-encoding"])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_generated_https_certificate(self):
        fixtures = {
            "rules": [
                {"match": {"method": "GET", "path": "/v1/status"}, "response": {"json": {"ok": True}}}
            ]
        }
        with tempfile.TemporaryDirectory() as directory:
            certificate, key = generate_certificate(Path(directory))
            server = ThreadingHTTPServer(("127.0.0.1", 0), build_handler(fixtures))
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(certificate, key)
            server.socket = context.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                response = send_request(
                    f"https://127.0.0.1:{server.server_port}",
                    {"method": "GET", "path": "/v1/status"},
                    timeout=2,
                    insecure=True,
                )
                self.assertEqual(response.status, 200)
                self.assertEqual(response.json_body, {"ok": True})
            finally:
                server.shutdown()
                server.server_close()
                thread.join(timeout=2)

    def test_runner_compares_two_endpoints_without_persisting_body(self):
        fixtures = {
            "rules": [
                {"match": {"method": "GET", "path": "/version"}, "response": {"json": {"version": "1"}}}
            ]
        }
        servers = [ThreadingHTTPServer(("127.0.0.1", 0), build_handler(fixtures)) for _ in range(2)]
        threads = [threading.Thread(target=server.serve_forever, daemon=True) for server in servers]
        for thread in threads:
            thread.start()
        try:
            with tempfile.TemporaryDirectory() as directory:
                manifest = Path(directory) / "manifest.json"
                output = Path(directory) / "report.json"
                manifest.write_text(
                    json.dumps(
                        {
                            "schema_version": 1,
                            "normalization": {"ignored_headers": ["date", "server"]},
                            "cases": [{"id": "version", "request": {"method": "GET", "path": "/version"}}],
                        }
                    )
                )
                original_argv = sys.argv
                sys.argv = [
                    "contract_runner.py",
                    "--manifest",
                    str(manifest),
                    "--left-url",
                    f"http://127.0.0.1:{servers[0].server_port}",
                    "--right-url",
                    f"http://127.0.0.1:{servers[1].server_port}",
                    "--output",
                    str(output),
                ]
                try:
                    self.assertEqual(contract_runner.main(), 0)
                finally:
                    sys.argv = original_argv
                report = json.loads(output.read_text())
                self.assertEqual(report["passed"], 1)
                self.assertNotIn("version", json.dumps(report["results"][0]["left"]))
        finally:
            for server in servers:
                server.shutdown()
                server.server_close()
            for thread in threads:
                thread.join(timeout=2)


if __name__ == "__main__":
    unittest.main()
