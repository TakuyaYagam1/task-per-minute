import json
import os
from pathlib import Path
import tempfile
import unittest

from bot_manifest import write_catalog


class BotManifestTest(unittest.TestCase):
    def test_control_volume_contains_no_answers(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            catalog, control = root / "catalog", root / "control"
            write_catalog(catalog, [{"id": "synthetic-task", "version": 1, "flag": "synthetic-answer"}], 1, control)
            self.assertEqual([p.name for p in control.iterdir()], ["control.key"])
            self.assertEqual([p.name for p in catalog.iterdir()], ["tasks.json"])
            self.assertEqual(control.stat().st_mode & 0o777, 0o700)

    def test_catalog_replaces_versions_and_retains_control_key(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            task = {"id": "synthetic-task", "version": 2, "flag": "synthetic-answer"}
            write_catalog(root, [task], 4)
            key = (root / "control.key").read_bytes()
            task["version"] = 3
            write_catalog(root, [task], 5)
            self.assertEqual(key, (root / "control.key").read_bytes())
            catalog = json.loads((root / "tasks.json").read_text())
            self.assertEqual(catalog["content_revision"], 5)
            self.assertEqual(catalog["tasks"][0]["version"], 3)
            self.assertEqual(os.stat(root / "tasks.json").st_mode & 0o777, 0o600)
            self.assertEqual(os.stat(root / "control.key").st_mode & 0o777, 0o600)

    def test_bad_catalog_keeps_previous_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            task = {"id": "synthetic-task", "version": 1, "flag": "synthetic-answer"}
            write_catalog(root, [task], 1)
            before = (root / "tasks.json").read_bytes()
            with self.assertRaises(ValueError):
                write_catalog(root, [task, task], 2)
            self.assertEqual(before, (root / "tasks.json").read_bytes())


if __name__ == "__main__":
    unittest.main()
