#!/usr/bin/env python3

import json
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
from controller_mock import build_handler, generate_certificate


def capture(body, headers=None):
    encoded = json.dumps(body).encode()
    return Capture(200, "OK", headers or {"content-type": ["application/json"]}, encoded, encoded, body)


class ContractLibraryTest(unittest.TestCase):
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
