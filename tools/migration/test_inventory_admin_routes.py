#!/usr/bin/env python3

import tempfile
import unittest
from pathlib import Path

from inventory_admin_routes import scan_file


class RouteInventoryTest(unittest.TestCase):
    def scan(self, source: str):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "admin/src/main/scala/com/neu/api/sample/SampleApi.scala"
            path.parent.mkdir(parents=True)
            path.write_text(source, encoding="utf-8")
            return scan_file(path, root)

    def test_nested_route_metadata(self):
        actions = self.scan(
            '''
            class SampleApi {
              private val item = "item"
              val route = headerValueByName("Token") { token =>
                pathPrefix("v1") {
                  path(item) {
                    get {
                      parameter(Symbol("id")) { id =>
                        optionalCookie("R_SESS") { cookie => complete(id) }
                      }
                    }
                  }
                }
              }
            }
            '''
        )
        self.assertEqual(len(actions), 1)
        self.assertEqual(actions[0]["method"], "GET")
        self.assertEqual(actions[0]["path"], "/v1/item")
        self.assertEqual(actions[0]["query_parameters"], ["id"])
        self.assertEqual(actions[0]["required_headers"], ["Token"])
        self.assertEqual(actions[0]["cookies"], ["R_SESS"])

    def test_combined_method_and_path(self):
        actions = self.scan(
            '''
            class SampleApi {
              private val login = "login"
              val route = (post & path(login)) { complete("ok") }
            }
            '''
        )
        self.assertEqual([(item["method"], item["path"]) for item in actions], [("POST", "/login")])

    def test_nested_duplicate_method_is_one_action(self):
        actions = self.scan(
            '''
            class SampleApi {
              val route = path("export") {
                post {
                  post { complete("ok") }
                }
              }
            }
            '''
        )
        self.assertEqual(len(actions), 1)
        self.assertEqual(actions[0]["dsl_lines"], [4, 5])

    def test_methodless_path_is_recorded_as_any(self):
        actions = self.scan(
            '''
            class SampleApi {
              val route = headerValueByName("Token") { token =>
                pathPrefix("workload") {
                  path("scanned") {
                    parameters(Symbol("start").?, Symbol("limit").?) { values => complete(values) }
                  }
                }
              }
            }
            '''
        )
        self.assertEqual(len(actions), 1)
        self.assertEqual(actions[0]["method"], "ANY")
        self.assertEqual(actions[0]["path"], "/workload/scanned")
        self.assertEqual(actions[0]["query_parameters"], ["limit", "start"])
        self.assertEqual(actions[0]["required_headers"], ["Token"])
        self.assertEqual(actions[0]["dsl_lines"], [])


if __name__ == "__main__":
    unittest.main()
