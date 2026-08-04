#!/usr/bin/env python3
"""Extract a deterministic HTTP route inventory from the Scala Pekko route DSL."""

from __future__ import annotations

import argparse
import json
import re
from collections import Counter
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


METHOD_RE = re.compile(r"(?:^|[\s(&])(get|post|put|patch|delete)\s*(?=\{|&)")
PATH_RE = re.compile(r"\b(path|pathPrefix)\(\s*(\"[^\"]+\"|[A-Za-z_]\w*)\s*\)")
CONST_RE = re.compile(r"\b(?:private\s+)?(?:final\s+)?val\s+(\w+)\s*=\s*\"([^\"]*)\"")
SYMBOL_RE = re.compile(r"Symbol\(\"([^\"]+)\"\)")
HEADER_RE = re.compile(r"(?:optional)?headerValueByName\(\"([^\"]+)\"\)")
COOKIE_RE = re.compile(r"(?:optional)?Cookie\((\"[^\"]+\"|[A-Za-z_]\w*)\)")


@dataclass
class Frame:
    path: tuple[str, ...] = ()
    headers: set[str] = field(default_factory=set)
    cookies: set[str] = field(default_factory=set)
    action: dict[str, Any] | None = None
    implicit_action: dict[str, Any] | None = None
    has_explicit_method: bool = False


def strip_line_comment(line: str) -> str:
    """Remove // comments while preserving markers inside quoted strings."""
    quoted = False
    escaped = False
    for index, char in enumerate(line):
        if escaped:
            escaped = False
            continue
        if char == "\\" and quoted:
            escaped = True
            continue
        if char == '"':
            quoted = not quoted
        elif char == "/" and not quoted and line[index : index + 2] == "//":
            return line[:index]
    return line


def brace_counts(line: str) -> tuple[int, int]:
    """Count structural braces outside strings."""
    opens = closes = 0
    quoted = escaped = False
    for char in line:
        if escaped:
            escaped = False
            continue
        if char == "\\" and quoted:
            escaped = True
            continue
        if char == '"':
            quoted = not quoted
        elif not quoted and char == "{":
            opens += 1
        elif not quoted and char == "}":
            closes += 1
    return opens, closes


def resolve(value: str, constants: dict[str, str]) -> str:
    if value.startswith('"'):
        return value[1:-1]
    return constants.get(value, f"{{{value}}}")


def current_state(frames: list[Frame]) -> tuple[list[str], set[str], set[str]]:
    path: list[str] = []
    headers: set[str] = set()
    cookies: set[str] = set()
    for frame in frames:
        path.extend(frame.path)
        headers.update(frame.headers)
        cookies.update(frame.cookies)
    return path, headers, cookies


def active_action(frames: list[Frame]) -> dict[str, Any] | None:
    for frame in reversed(frames):
        if frame.action is not None:
            return frame.action
    return None


def domain_for(path: Path) -> str:
    if path.parent.name == "authentication":
        return "authentication"
    if path.parent.name == "api":
        return "shared"
    return path.parent.name


def scan_file(source: Path, root: Path) -> list[dict[str, Any]]:
    text = source.read_text(encoding="utf-8-sig")
    constants = dict(CONST_RE.findall(text))
    frames: list[Frame] = []
    actions: list[dict[str, Any]] = []

    def pop_frame() -> None:
        frame = frames.pop()
        if frame.implicit_action is not None and not frame.has_explicit_method:
            actions.append(frame.implicit_action)

    for line_number, raw_line in enumerate(text.splitlines(), start=1):
        line = strip_line_comment(raw_line)
        leading_closes = len(line) - len(line.lstrip().lstrip("}"))
        # The expression above counts indentation too, so use an explicit match.
        close_match = re.match(r"\s*(}*)", line)
        leading_closes = len(close_match.group(1)) if close_match else 0
        for _ in range(min(leading_closes, len(frames))):
            pop_frame()

        working = re.sub(r"^\s*}+", "", line)
        inherited_path, inherited_headers, inherited_cookies = current_state(frames)
        path_additions = [resolve(match.group(2), constants) for match in PATH_RE.finditer(working)]
        headers = set(HEADER_RE.findall(working))
        cookies = {resolve(match.group(1), constants) for match in COOKIE_RE.finditer(working)}
        query = set(SYMBOL_RE.findall(working))

        action = active_action(frames)
        if action is not None:
            action["query_parameters"] = sorted(set(action["query_parameters"]) | query)
            action["required_headers"] = sorted(set(action["required_headers"]) | headers)
            action["cookies"] = sorted(set(action["cookies"]) | cookies)
        else:
            for frame in reversed(frames):
                if frame.implicit_action is not None:
                    candidate = frame.implicit_action
                    candidate["query_parameters"] = sorted(set(candidate["query_parameters"]) | query)
                    candidate["required_headers"] = sorted(set(candidate["required_headers"]) | headers)
                    candidate["cookies"] = sorted(set(candidate["cookies"]) | cookies)
                    break

        method_match = METHOD_RE.search(working)
        new_action: dict[str, Any] | None = None
        if method_match:
            for frame in frames:
                frame.has_explicit_method = True
            full_path = inherited_path + path_additions
            route_path = "/" + "/".join(segment.strip("/") for segment in full_path if segment)
            method = method_match.group(1).upper()
            if action is not None and action["method"] == method and action["path"] == route_path:
                # A repeated nested directive, such as post { post { ... } }, is one HTTP action.
                action["dsl_lines"].append(line_number)
            else:
                new_action = {
                    "domain": domain_for(source),
                    "method": method,
                    "path": route_path or "/",
                    "query_parameters": sorted(query),
                    "required_headers": sorted(inherited_headers | headers),
                    "cookies": sorted(inherited_cookies | cookies),
                    "source": str(source.relative_to(root)),
                    "line": line_number,
                    "dsl_lines": [line_number],
                }
                actions.append(new_action)

        implicit_action: dict[str, Any] | None = None
        path_matches = list(PATH_RE.finditer(working))
        if method_match is None and any(match.group(1) == "path" for match in path_matches):
            full_path = inherited_path + path_additions
            route_path = "/" + "/".join(segment.strip("/") for segment in full_path if segment)
            implicit_action = {
                "domain": domain_for(source),
                "method": "ANY",
                "path": route_path or "/",
                "query_parameters": sorted(query),
                "required_headers": sorted(inherited_headers | headers),
                "cookies": sorted(inherited_cookies | cookies),
                "source": str(source.relative_to(root)),
                "line": line_number,
                "dsl_lines": [],
                "implicit_method": True,
            }

        opens, closes = brace_counts(working)
        remaining_closes = max(0, closes - leading_closes)
        if opens:
            frames.append(
                Frame(
                    path=tuple(path_additions),
                    headers=headers,
                    cookies=cookies,
                    action=new_action,
                    implicit_action=implicit_action,
                )
            )
            for _ in range(opens - 1):
                frames.append(Frame())
        for _ in range(min(remaining_closes, len(frames))):
            pop_frame()

    while frames:
        pop_frame()

    return actions


def markdown(actions: list[dict[str, Any]]) -> str:
    counts = Counter(action["domain"] for action in actions)
    lines = [
        "# Scala Admin Route Inventory",
        "",
        "> Generated by `tools/migration/inventory_admin_routes.py`; do not edit manually.",
        "",
        f"Total semantic route actions: **{len(actions)}**",
        "",
        f"Total HTTP method DSL occurrences: **{sum(len(action['dsl_lines']) for action in actions)}**",
        "",
        "## Domain Summary",
        "",
        "| Domain | Actions |",
        "| --- | ---: |",
    ]
    lines.extend(f"| {domain} | {count} |" for domain, count in sorted(counts.items()))
    lines.extend(
        [
            "",
            "## Actions",
            "",
            "| Route ID | Domain | Method | Path | Query | Headers | Cookies | Source |",
            "| --- | --- | --- | --- | --- | --- | --- | --- |",
        ]
    )
    for action in actions:
        query = ", ".join(action["query_parameters"])
        headers = ", ".join(action["required_headers"])
        cookies = ", ".join(action["cookies"])
        source = f"{action['source']}:{action['line']}"
        lines.append(
            f"| `{action['route_id']}` | {action['domain']} | {action['method']} | `{action['path']}` | "
            f"{query} | {headers} | {cookies} | `{source}` |"
        )
    return "\n".join(lines) + "\n"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--source",
        type=Path,
        default=Path("admin/src/main/scala/com/neu/api"),
        help="Scala API source directory",
    )
    parser.add_argument("--json", type=Path, help="Write JSON inventory to this path")
    parser.add_argument("--markdown", type=Path, help="Write Markdown inventory to this path")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    repo_root = Path.cwd().resolve()
    source_root = args.source.resolve()
    files = sorted(source_root.rglob("*Api.scala"))
    if not files:
        raise SystemExit(f"no *Api.scala files found under {source_root}")

    actions: list[dict[str, Any]] = []
    for source in files:
        actions.extend(scan_file(source, repo_root))
    actions.sort(key=lambda item: (item["domain"], item["path"], item["method"], item["source"], item["line"]))
    route_key_counts = Counter(f"{item['domain']}:{item['method']}:{item['path']}" for item in actions)
    route_key_seen: Counter[str] = Counter()
    for action in actions:
        key = f"{action['domain']}:{action['method']}:{action['path']}"
        route_key_seen[key] += 1
        action["route_id"] = key if route_key_counts[key] == 1 else f"{key}#{route_key_seen[key]}"

    inventory = {
        "schema_version": 1,
        "source": str(args.source),
        "action_count": len(actions),
        "dsl_method_occurrence_count": sum(len(action["dsl_lines"]) for action in actions),
        "actions": actions,
    }
    rendered = json.dumps(inventory, ensure_ascii=False, indent=2) + "\n"
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(rendered, encoding="utf-8")
    else:
        print(rendered, end="")
    if args.markdown:
        args.markdown.parent.mkdir(parents=True, exist_ok=True)
        args.markdown.write_text(markdown(actions), encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
