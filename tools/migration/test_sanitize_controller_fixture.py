#!/usr/bin/env python3

import unittest

from sanitize_controller_fixture import sanitize, unsafe_values


class SanitizeControllerFixtureTest(unittest.TestCase):
    def test_sanitizes_secrets_and_preserves_relational_pseudonyms(self):
        source = {
            "token": "a" * 40,
            "users": [
                {"id": "customer-one", "name": "Alice", "email": "alice@customer.test"},
                {"id": "customer-one", "name": "Alice", "ip": "10.1.2.3"},
            ],
            "url": "https://controller.customer.test/path",
        }
        output = sanitize(source, b"x" * 32)
        self.assertEqual(output["token"], "<redacted>")
        self.assertEqual(output["users"][0]["id"], output["users"][1]["id"])
        self.assertTrue(output["users"][0]["email"].endswith("@example.invalid"))
        self.assertTrue(output["users"][1]["ip"].startswith("192.0.2."))
        self.assertEqual(output["url"], "https://service.example.invalid")
        self.assertEqual(unsafe_values(output), [])

    def test_scanner_rejects_unsanitized_sensitive_fields(self):
        findings = unsafe_values({"password": "secret", "email": "real@customer.test"})
        self.assertEqual(findings, ["/email", "/password"])


if __name__ == "__main__":
    unittest.main()
