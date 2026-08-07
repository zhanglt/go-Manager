import hashlib
import json
import tempfile
import unittest
from pathlib import Path

from verify_oci_attestations import PROVENANCE_PREDICATE, SBOM_PREDICATE, predicate_types


class OCIBuilder:
    def __init__(self, root: Path):
        self.root = root
        (root / "blobs" / "sha256").mkdir(parents=True)

    def blob(self, value: dict) -> tuple[str, int]:
        content = json.dumps(value, separators=(",", ":")).encode()
        digest = hashlib.sha256(content).hexdigest()
        (self.root / "blobs" / "sha256" / digest).write_bytes(content)
        return f"sha256:{digest}", len(content)

    def add_attestation(self, predicate: str) -> dict:
        statement, statement_size = self.blob({"predicateType": predicate})
        manifest, manifest_size = self.blob(
            {
                "schemaVersion": 2,
                "layers": [
                    {
                        "mediaType": "application/vnd.in-toto+json",
                        "digest": statement,
                        "size": statement_size,
                    }
                ],
            }
        )
        return {
            "mediaType": "application/vnd.oci.image.manifest.v1+json",
            "digest": manifest,
            "size": manifest_size,
            "annotations": {"vnd.docker.reference.type": "attestation-manifest"},
        }


class VerifyOCIAttestationsTest(unittest.TestCase):
    def test_finds_only_referenced_attestation_predicates(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            builder = OCIBuilder(root)
            manifests = [
                builder.add_attestation(SBOM_PREDICATE),
                builder.add_attestation(PROVENANCE_PREDICATE),
            ]
            builder.blob({"predicateType": "https://example.invalid/unreferenced"})
            (root / "index.json").write_text(json.dumps({"manifests": manifests}))

            self.assertEqual(predicate_types(root), {SBOM_PREDICATE, PROVENANCE_PREDICATE})

    def test_finds_attestations_in_nested_buildx_index(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            builder = OCIBuilder(root)
            nested_index, nested_index_size = builder.blob(
                {
                    "schemaVersion": 2,
                    "manifests": [
                        builder.add_attestation(SBOM_PREDICATE),
                        builder.add_attestation(PROVENANCE_PREDICATE),
                    ],
                }
            )
            (root / "index.json").write_text(
                json.dumps(
                    {
                        "manifests": [
                            {
                                "mediaType": "application/vnd.oci.image.index.v1+json",
                                "digest": nested_index,
                                "size": nested_index_size,
                            }
                        ]
                    }
                )
            )

            self.assertEqual(predicate_types(root), {SBOM_PREDICATE, PROVENANCE_PREDICATE})

    def test_rejects_malformed_digest(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "index.json").write_text(
                json.dumps(
                    {
                        "manifests": [
                            {
                                "mediaType": "application/vnd.oci.image.manifest.v1+json",
                                "digest": "sha256:unsafe",
                                "annotations": {
                                    "vnd.docker.reference.type": "attestation-manifest"
                                },
                            }
                        ]
                    }
                )
            )
            with self.assertRaises(ValueError):
                predicate_types(root)


if __name__ == "__main__":
    unittest.main()
