#!/usr/bin/env python3
import sys
sys.dont_write_bytecode = True

import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("release", Path(__file__).with_name("release-notes.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseNotesTest(unittest.TestCase):
    changelog = """# Changelog
## [Unreleased]
Pending work.
## [v2.0.0-rc.1]
### Added
- 新功能。
## [v1.5.0] — 2026-11-01
### Fixed
- Fix.
"""

    def test_exact_version_and_last_section(self):
        self.assertEqual(release.release_notes(self.changelog, "v2.0.0-rc.1"), "### Added\n- 新功能。\n")
        self.assertEqual(release.release_notes(self.changelog, "v1.5.0"), "### Fixed\n- Fix.\n")

    def test_missing_unreleased_empty_and_duplicate(self):
        for text, version in [
            (self.changelog, "v1.5"),
            (self.changelog, "Unreleased"),
            ("## [v1.5.0]\n\n## [Unreleased]\nPending", "v1.5.0"),
            (self.changelog + "\n## [v1.5.0]\nDuplicate", "v1.5.0"),
        ]:
            with self.subTest(version=version, text=text):
                with self.assertRaises(ValueError):
                    release.release_notes(text, version)


if __name__ == "__main__":
    unittest.main()
