# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class OpenVEXValidationTests(unittest.TestCase):
    def validate(self, document):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "openvex.json"
            path.write_text(json.dumps(document))
            return subprocess.run(
                ["bash", str(ROOT / "hack/validate-openvex.sh"), str(path)],
                capture_output=True,
                text=True,
            )

    def test_committed_document(self):
        result = self.validate(json.loads((ROOT / ".openvex.json").read_text()))
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_supported_string_statuses(self):
        original = json.loads((ROOT / ".openvex.json").read_text())
        for status in ["affected", "not_affected", "fixed", "under_investigation"]:
            with self.subTest(status=status):
                document = copy.deepcopy(original)
                document["statements"][-1]["status"] = status
                if status == "affected":
                    document["statements"][-1]["action_statement"] = "Upgrade to the patched release."
                result = self.validate(document)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_missing_scope_or_evidence(self):
        original = json.loads((ROOT / ".openvex.json").read_text())
        for field, value in [
            ("products", []),
            ("products", [{"@id": "pkg:oci/controller"}]),
            ("impact_statement", ""),
            ("status", "approved"),
            ("status", ["affected"]),
            ("justification", "no_risk"),
            ("justification", ["vulnerable_code_not_present"]),
            ("vulnerability", {}),
        ]:
            with self.subTest(field=field, value=value):
                document = copy.deepcopy(original)
                # A bad final statement must fail even after valid ones pass.
                document["statements"][-1][field] = value
                self.assertNotEqual(self.validate(document).returncode, 0)

    def test_rejects_empty_or_malformed_document(self):
        original = json.loads((ROOT / ".openvex.json").read_text())
        for document in [[], {}, {**original, "statements": []}, {**original, "version": True}]:
            with self.subTest(document_type=type(document).__name__):
                self.assertNotEqual(self.validate(document).returncode, 0)


if __name__ == "__main__":
    unittest.main()
