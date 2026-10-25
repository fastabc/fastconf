#!/usr/bin/env python3
import sys
sys.dont_write_bytecode = True

import importlib.util
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("links", Path(__file__).with_name("check-doc-links.py"))
links = importlib.util.module_from_spec(spec)
spec.loader.exec_module(links)


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


if __name__ == "__main__":
    unittest.main()
