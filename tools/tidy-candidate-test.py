#!/usr/bin/env python3
"""Offline regression tests for candidate checksum merging."""

import importlib.util
from pathlib import Path
import unittest
import sys

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location("tidy_candidate", Path(__file__).with_name("tidy-candidate.py"))
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)


def is_candidate(line):
    return line.startswith("github.com/fastabc/fastconf v1.2.0 ") or line.startswith("github.com/fastabc/fastconf v1.2.0/go.mod ")


class MergeSumsTest(unittest.TestCase):
    def test_merge_preserves_go_order(self):
        ordinary = "ex v1.9.0/go.mod h1:a=\nex v1.14.1 h1:b=\n"
        real = "github.com/fastabc/fastconf v1.2.0 h1:REAL=\n"
        self.assertEqual(mod.merge_sums(real, ordinary + "github.com/fastabc/fastconf v1.2.0 h1:SYNTH=\n", is_candidate), ordinary + real)

    def test_merge_drops_synthetic_without_original(self):
        self.assertEqual(mod.merge_sums("", "github.com/fastabc/fastconf v1.2.0 h1:SYNTH=\n", is_candidate), "")

    def test_merge_matches_exact_module_and_version(self):
        original = "github.com/fastabc/fastconf v1.2.0 h1:REAL=\ngithub.com/fastabc/fastconf v1.2.0/go.mod h1:MOD=\n"
        updated = "github.com/fastabc/fastconf v1.1.0 h1:OLD=\ngithub.com/fastabc/fastconf v1.2.0/go.mod h1:SYNTH=\ngithub.com/fastabc/fastconf/sub v1.2.0 h1:OTHER=\n"
        want = updated.replace("h1:SYNTH=", "h1:MOD=")
        self.assertEqual(mod.merge_sums(original, updated, is_candidate), want)


if __name__ == "__main__":
    unittest.main()
