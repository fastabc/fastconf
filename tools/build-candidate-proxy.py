"""Copy the checkout and build its synthetic Go module proxy."""

import json
import pathlib
import re
import shutil
import subprocess
import sys
import zipfile

root, temp = map(pathlib.Path, sys.argv[1:3])
version = sys.argv[3]
source_version = sys.argv[4]
checkout = temp / "checkout"
modules = subprocess.check_output(["bash", str(root / "tools/modules.sh"), "list"], text=True).splitlines()
nested = [m for m in modules if m != "."]
candidate_sums = {
    ("github.com/fastabc/fastconf" + ("" if m == "." else "/" + m), v)
    for m in modules for v in (version, version + "/go.mod")
}
files = subprocess.check_output([
    "git", "-C", str(root), "ls-files", "--cached", "--others",
    "--exclude-standard", "-z",
]).decode().split("\0")

def owner(name):
    """Innermost inventory module containing name."""
    return max((m for m in nested if name.startswith(m + "/")), key=len, default=".")

# Every inventory module is served at the candidate version, so satellites may
# pin each other (e.g. log adapters -> integrations/log) before publication.
archives = {}
for name in sorted(set(files)):
    source = root / name
    if not name or not source.is_file() or source.is_symlink():
        continue
    target = checkout / name
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, target)
    # Retain the standalone comparison workspace, but never ship it inside
    # the root module's synthetic archive or treat it as a release module.
    if name.startswith("benchmarks/"):
        continue
    module = owner(name)
    if source_version != version and name == ("go.mod" if module == "." else module + "/go.mod"):
        # Rewrite only inventory-module pins in the disposable API candidate.
        def rewrite_pin(match):
            prefix, path, space, pinned = match.groups()
            if pinned == source_version and (path, version) in candidate_sums:
                pinned = version
            return prefix + path + space + pinned
        target.write_text(re.sub(r"(?m)^([ \t]*(?:require[ \t]+)?)(\S+)([ \t]+)(\S+)", rewrite_pin, target.read_text()))
    if name == ("go.sum" if module == "." else module + "/go.sum"):
        # Published sums describe different archives from this synthetic proxy.
        # Regenerate only the discovered candidates, in the disposable copy;
        # retain third-party checksums and other versions for verification.
        target.write_text("".join(
            line for line in target.read_text().splitlines(keepends=True)
            if tuple(line.split()[:2]) not in candidate_sums
        ))
    path = "github.com/fastabc/fastconf" + ("" if module == "." else "/" + module)
    if path not in archives:
        proxy = temp / "proxy" / path / "@v"
        proxy.mkdir(parents=True)
        archives[path] = (zipfile.ZipFile(proxy / (version + ".zip"), "w", zipfile.ZIP_DEFLATED), proxy, module)
    rel = name if module == "." else name[len(module) + 1:]
    archives[path][0].write(target, path + "@" + version + "/" + rel)
for path, (archive, proxy, module) in archives.items():
    archive.close()
    shutil.copy2(checkout / module / "go.mod", proxy / (version + ".mod"))
    (proxy / (version + ".info")).write_text(json.dumps({
        "Version": version, "Time": "2026-09-28T00:00:00Z",
    }))
    # Do not serve a version list or @latest: gorelease's baseline must resolve
    # from the published upstream proxy, never from this synthetic candidate.
