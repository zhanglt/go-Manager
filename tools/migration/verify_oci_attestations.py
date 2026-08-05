#!/usr/bin/env python3
"""Verify that an OCI layout contains BuildKit SBOM and provenance attestations."""

from __future__ import annotations

import argparse
import json
import tarfile
from pathlib import Path
from typing import Any


SBOM_PREDICATE = "https://spdx.dev/Document"
PROVENANCE_PREDICATE = "https://slsa.dev/provenance/v1"
REQUIRED_PREDICATES = {SBOM_PREDICATE, PROVENANCE_PREDICATE}
ATTESTATION_MANIFEST = "application/vnd.oci.image.manifest.v1+json"
IN_TOTO_LAYER = "application/vnd.in-toto+json"
IMAGE_INDEXES = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}


class OCIReader:
    def __init__(self, source: Path):
        self.source = source
        self.archive = tarfile.open(source, "r:*") if source.is_file() else None

    def close(self) -> None:
        if self.archive is not None:
            self.archive.close()

    def read(self, name: str) -> bytes:
        if self.archive is None:
            return (self.source / name).read_bytes()
        member = self.archive.getmember(name)
        stream = self.archive.extractfile(member)
        if stream is None:
            raise ValueError(f"OCI archive member is not a file: {name}")
        return stream.read()

    def json(self, name: str) -> dict[str, Any]:
        value = json.loads(self.read(name))
        if not isinstance(value, dict):
            raise ValueError(f"expected JSON object: {name}")
        return value


def blob_path(digest: str) -> str:
    algorithm, separator, value = digest.partition(":")
    if separator != ":" or algorithm != "sha256" or len(value) != 64:
        raise ValueError(f"unsupported OCI digest: {digest}")
    return f"blobs/{algorithm}/{value}"


def predicate_types(source: Path) -> set[str]:
    reader = OCIReader(source)
    try:
        index = reader.json("index.json")
        predicates: set[str] = set()
        visited: set[str] = set()

        def visit(descriptor: dict[str, Any]) -> None:
            digest = str(descriptor.get("digest", ""))
            if not digest or digest in visited:
                return
            visited.add(digest)

            document = reader.json(blob_path(digest))
            if descriptor.get("mediaType") in IMAGE_INDEXES:
                for child in document.get("manifests", []):
                    if isinstance(child, dict):
                        visit(child)
                return

            annotations = descriptor.get("annotations", {})
            if (
                descriptor.get("mediaType") != ATTESTATION_MANIFEST
                or annotations.get("vnd.docker.reference.type")
                != "attestation-manifest"
            ):
                return
            for layer in document.get("layers", []):
                if layer.get("mediaType") != IN_TOTO_LAYER:
                    continue
                statement = reader.json(blob_path(str(layer["digest"])))
                predicate = statement.get("predicateType")
                if isinstance(predicate, str):
                    predicates.add(predicate)

        for descriptor in index.get("manifests", []):
            if isinstance(descriptor, dict):
                visit(descriptor)
        return predicates
    finally:
        reader.close()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("layout", type=Path, help="OCI layout directory or tar archive")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    found = predicate_types(args.layout)
    missing = sorted(REQUIRED_PREDICATES - found)
    if missing:
        print(f"missing OCI attestations: {', '.join(missing)}")
        return 1
    print(f"verified OCI attestations: {', '.join(sorted(REQUIRED_PREDICATES))}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
