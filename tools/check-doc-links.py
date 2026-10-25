#!/usr/bin/env python3
"""Check local file links in repository Markdown, without fetching URLs."""
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

# Ignore fenced examples and inline code before recognizing Markdown links.
FENCE = re.compile(r"^\s{0,3}(`{3,}|~{3,})")
LINK = re.compile(r'!?\[[^\]\n]*\]\(\s*(<[^>\n]+>|[^\s)]+)(?:\s+"[^"\n]*")?\s*\)')
REFERENCE = re.compile(r'^\s{0,3}\[[^\]]+\]:\s*(<[^>]+>|\S+)')


def destinations(markdown):
    fence = None
    for line in markdown.splitlines():
        match = FENCE.match(line)
        if match:
            marker = match[1]
            if fence is None:
                fence = marker
            elif marker[0] == fence[0] and len(marker) >= len(fence):
                fence = None
            continue
        if fence is not None:
            continue
        line = re.sub(r"(`+).*?\1", "", line)
        for match in LINK.finditer(line):
            yield match[1].strip("<>")
        match = REFERENCE.match(line)
        if match:
            yield match[1].strip("<>")


def missing_links(root, files):
    for file in files:
        source = root / file
        if not source.is_file():  # Include unstaged moves/deletions.
            continue
        for destination in destinations(source.read_text(encoding="utf-8")):
            parsed = urlsplit(destination)
            if parsed.scheme or parsed.netloc or not parsed.path:
                continue
            path = unquote(parsed.path)
            target = root / path.lstrip("/") if path.startswith("/") else source.parent / path
            if not target.exists():
                yield f"{file}: missing {destination}"


def main():
    root = Path(__file__).resolve().parent.parent
    files = sys.argv[1:]
    if not files:
        output = subprocess.check_output([
            "git", "-C", str(root), "ls-files", "--cached", "--others",
            "--exclude-standard", "-z", "--", "*.md",
        ])
        files = sorted(set(output.decode().rstrip("\0").split("\0")))
    errors = list(missing_links(root, files))
    for error in errors:
        print(error, file=sys.stderr)
    return bool(errors)


if __name__ == "__main__":
    sys.exit(main())
