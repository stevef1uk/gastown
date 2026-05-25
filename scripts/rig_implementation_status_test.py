#!/usr/bin/env python3
"""Tests for rig_implementation_status bead matching."""

import unittest
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import rig_implementation_status as ris  # noqa: E402


def linkshelf_val() -> dict:
    return {
        "layout_root": "linkshelf",
        "bead_title_contains": "Implement linkshelf/",
        "required_files": [
            "linkshelf/internal/store/schema.go",
            "linkshelf/go.mod",
            "linkshelf/internal/store/store.go",
            "linkshelf/internal/store/store_test.go",
        ],
    }


class TestBeadMatching(unittest.TestCase):
    def test_matches_planner_title_without_layout_prefix(self) -> None:
        title = "Implement internal/store/schema.go per architecture"
        self.assertTrue(ris.matches_implement_bead_title(title, linkshelf_val()))

    def test_matches_canonical_title(self) -> None:
        title = "Implement linkshelf/go.mod per architecture"
        self.assertTrue(ris.matches_implement_bead_title(title, linkshelf_val()))

    def test_rejects_agent_bead(self) -> None:
        title = "Architect for testgt3 - manages high-level design"
        self.assertFalse(ris.matches_implement_bead_title(title, linkshelf_val()))

    def test_extract_and_normalize_path(self) -> None:
        title = "Implement internal/store/schema.go per architecture"
        val = linkshelf_val()
        raw = ris.extract_path_from_bead_title(title, val["bead_title_contains"])
        self.assertEqual(raw, "internal/store/schema.go")
        norm = ris.normalize_bead_path(raw, val["layout_root"], val["required_files"])
        self.assertEqual(norm, "linkshelf/internal/store/schema.go")

    def test_parse_bd_flat_line(self) -> None:
        line = "○ te-s73 [● P2] [task] - Implement internal/store/schema.go per architecture"
        row = ris.parse_bd_flat_line(line)
        self.assertIsNotNone(row)
        assert row is not None
        self.assertEqual(row["id"], "te-s73")
        self.assertEqual(row["status"], "open")

    def test_parse_bd_flat_lines_filters_implement(self) -> None:
        text = "\n".join(
            [
                "○ te-s73 [● P2] [task] - Implement internal/store/schema.go per architecture",
                "○ te-testgt3-qa [● P2] [task] - QA for testgt3",
            ]
        )
        rows = ris.parse_bd_flat_lines(text, linkshelf_val())
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["id"], "te-s73")


if __name__ == "__main__":
    unittest.main()
