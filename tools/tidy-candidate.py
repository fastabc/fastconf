#!/usr/bin/env python3
"""Tidy inside check-candidate's disposable checkout, excluding synthetic sums."""

import difflib
from pathlib import Path
import subprocess
import sys


def read(path):
    return path.read_text() if path.exists() else ""


def merge_sums(original, updated, is_candidate):
    """Keep Go's order, replacing synthetic sums only with original hashes."""
    real = {tuple(line.split()[:2]): line
            for line in original.splitlines(keepends=True) if is_candidate(line)}
    out = []
    for line in updated.splitlines(keepends=True):
        if not is_candidate(line):
            out.append(line)
        elif tuple(line.split()[:2]) in real:
            out.append(real[tuple(line.split()[:2])])
    return "".join(out)


def main():
    source = Path(sys.argv[1])
    version, mode = sys.argv[2:4]
    modules = sys.argv[4:]
    release_modules = subprocess.check_output(
        ["bash", "tools/modules.sh", "list"], text=True
    ).splitlines()
    # Match exact discovered module paths, never a prefix that could hide an
    # unrelated external submodule or an older published version.
    candidate_paths = {
        line.split()[1]
        for module in release_modules
        for line in Path(module, "go.mod").read_text().splitlines()
        if line.startswith("module ")
    }

    def is_candidate(line):
        fields = line.split()
        return len(fields) >= 2 and fields[0] in candidate_paths and fields[1] in (
            version, version + "/go.mod"
        )

    def ordinary_sums(text):
        return "".join(line for line in text.splitlines(keepends=True) if not is_candidate(line))

    changes = []
    failed = False
    for module in modules:
        print(f"modules: {mode} / {module}", flush=True)
        if subprocess.run(["go", "mod", "tidy"], cwd=module).returncode:
            failed = True
            continue
        for name in ("go.mod", "go.sum"):
            relative = Path(module, name)
            original = read(source / relative)
            updated = read(relative)
            before = ordinary_sums(original) if name == "go.sum" else original
            after = ordinary_sums(updated) if name == "go.sum" else updated
            if before != after:
                if mode == "tidy-check":
                    sys.stdout.writelines(difflib.unified_diff(
                        before.splitlines(keepends=True), after.splitlines(keepends=True),
                        fromfile=str(relative), tofile=str(relative) + " (tidy)",
                    ))
                else:
                    # Retain original candidate checksums only if tidy still
                    # needs that exact entry; never copy a synthetic checksum.
                    if name == "go.sum":
                        after = merge_sums(original, updated, is_candidate)
                    changes.append((source / relative, after))
                failed = failed or mode == "tidy-check"
    if failed:
        print(f"Resolve errors above, then run make tidy VERSION={version}.", file=sys.stderr)
        return 1
    for path, content in changes:
        if content:
            path.write_text(content)
        elif path.exists():
            path.unlink()
    return 0


if __name__ == "__main__":
    sys.exit(main())
