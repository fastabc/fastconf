#!/usr/bin/env python3
"""Regression tests for the repository's Python tools."""

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True


def load_tool(name):
    path = Path(__file__).resolve().parents[1] / (name + ".py")
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


links = load_tool("check-doc-links")
release = load_tool("release-notes")
tidy = load_tool("tidy-candidate")


class LocalLinksTest(unittest.TestCase):
    def test_examples_and_urls(self):
        text = """[file](../a.md#heading) [external](https://example.test/no)
`[inline](missing)`
```md
[example](missing)
```
~~~
[example](missing)
~~~
[reference]: <a b.md>
![image](image.png)
"""
        self.assertEqual(list(links.destinations(text)), [
            "../a.md#heading", "https://example.test/no", "a b.md", "image.png",
        ])

    def test_resolution_and_missing_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs").mkdir()
            (root / "a b.md").touch()
            (root / "docs/page.md").write_text(
                "[relative](../a%20b.md#heading) [root](/a%20b.md) "
                "[anchor](#heading) [broken](missing.md) [url](https://example.test/no)"
            )
            self.assertEqual(list(links.missing_links(root, ["docs/page.md", "deleted.md"])),
                             ["docs/page.md: missing missing.md"])


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


def is_candidate(line):
    return line.startswith("github.com/fastabc/fastconf v1.2.0 ") or line.startswith("github.com/fastabc/fastconf v1.2.0/go.mod ")


class MergeSumsTest(unittest.TestCase):
    def test_merge_preserves_go_order(self):
        ordinary = "ex v1.9.0/go.mod h1:a=\nex v1.14.1 h1:b=\n"
        real = "github.com/fastabc/fastconf v1.2.0 h1:REAL=\n"
        self.assertEqual(tidy.merge_sums(real, ordinary + "github.com/fastabc/fastconf v1.2.0 h1:SYNTH=\n", is_candidate), ordinary + real)

    def test_merge_drops_synthetic_without_original(self):
        self.assertEqual(tidy.merge_sums("", "github.com/fastabc/fastconf v1.2.0 h1:SYNTH=\n", is_candidate), "")

    def test_merge_matches_exact_module_and_version(self):
        original = "github.com/fastabc/fastconf v1.2.0 h1:REAL=\ngithub.com/fastabc/fastconf v1.2.0/go.mod h1:MOD=\n"
        updated = "github.com/fastabc/fastconf v1.1.0 h1:OLD=\ngithub.com/fastabc/fastconf v1.2.0/go.mod h1:SYNTH=\ngithub.com/fastabc/fastconf/sub v1.2.0 h1:OTHER=\n"
        want = updated.replace("h1:SYNTH=", "h1:MOD=")
        self.assertEqual(tidy.merge_sums(original, updated, is_candidate), want)


if __name__ == "__main__":
    unittest.main()
