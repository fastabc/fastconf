#!/usr/bin/env python3
"""Extract one release's notes from CHANGELOG.md; never fall back to Unreleased."""
import argparse
import re
from pathlib import Path


def release_notes(changelog, version):
    headings = list(re.finditer(r"^## \[([^\]\n]+)\][^\n]*$", changelog, re.MULTILINE))
    matches = [i for i, heading in enumerate(headings) if heading[1] == version]
    if version == "Unreleased" or len(matches) != 1:
        raise ValueError(f"expected exactly one changelog section for {version}")
    index = matches[0]
    end = headings[index + 1].start() if index + 1 < len(headings) else len(changelog)
    notes = changelog[headings[index].end():end].strip()
    if not notes:
        raise ValueError(f"empty changelog section for {version}")
    return notes + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("--changelog", type=Path, default=Path(__file__).resolve().parent.parent / "CHANGELOG.md")
    args = parser.parse_args()
    try:
        notes = release_notes(args.changelog.read_text(encoding="utf-8"), args.version)
    except ValueError as error:
        parser.error(str(error))
    print(notes, end="")


if __name__ == "__main__":
    main()
