import argparse
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest import mock

import corpus


class RunIntegrityTests(unittest.TestCase):
    def fixture(self, root):
        data = corpus.canonical({"Resources": {"Thing": {"Type": "AWS::Test::Thing"}}})
        original, normalized = root / "original.yaml", root / "normalized.json"
        original.write_bytes(data)
        normalized.write_bytes(data)
        record = dict(id="test", source="fixture", path="example.yaml", url="https://example.invalid/template",
                      original="original.yaml", normalized="normalized.json", resource_count=1,
                      resource_types=["AWS::Test::Thing"], intrinsics={},
                      original_sha256=corpus.digest(data), normalized_sha256=corpus.digest(data))
        (root / "index.json").write_bytes(corpus.canonical(dict(templates=[record], normalizer="fixture", source_lock_sha256="fixture")))
        schemas = root / "schemas"
        schemas.mkdir()
        (schemas / "manifest.json").write_bytes(corpus.canonical(dict(sourceSHA256="fixture", regions={"us-east-1": {}, "us-west-2": {}})))
        (schemas / "test.cue").write_text("package fixture\n")
        validator = root / "validator"
        validator.write_bytes(b"validator fixture")
        return argparse.Namespace(corpus=root, schemas=schemas, validator=validator, report=root / "report",
                                  source=None, ids=None, limit=0, regions="us-east-1,us-west-2", expect=None,
                                  timeout=1, memory="2GiB", resources="all", max_resource_checks=0,
                                  resume=False, fail_on_disagreement=False, checks="structural")

    def run_fixture(self, args, lint=None, cue=None):
        with mock.patch.object(corpus, "lint", side_effect=lint, return_value={"status": "valid"}), \
             mock.patch.object(corpus, "cue", side_effect=cue, return_value={"status": "valid"}), \
             contextlib.redirect_stdout(io.StringIO()):
            corpus.run(args)

    def test_content_verification_is_once_per_template(self):
        with tempfile.TemporaryDirectory() as temporary:
            args = self.fixture(Path(temporary))
            original_read = Path.read_bytes
            with mock.patch.object(corpus, "files_digest", wraps=corpus.files_digest) as hashes, \
                 mock.patch.object(Path, "read_bytes", autospec=True, side_effect=original_read) as reads:
                self.run_fixture(args)
            groups = [call.args[0] for call in hashes.call_args_list]
            self.assertEqual(groups.count([args.validator]), 2)  # startup and final content check
            self.assertEqual(groups.count([args.corpus / "original.yaml"]), 1)
            self.assertEqual(sum(call.args[0] == args.corpus / "normalized.json" for call in reads.call_args_list), 1)
            report = json.loads((args.report / "report.json").read_bytes())
            self.assertEqual(len(report["results"]), 2)
            self.assertTrue(all(r["classification"] == "both-accept" for r in report["results"]))

    def test_tool_rebuild_between_engines_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            args = self.fixture(Path(temporary))
            def change_tool(*unused):
                args.validator.write_bytes(b"rebuilt validator")
                return {"status": "valid"}
            with self.assertRaisesRegex(ValueError, "validator or harness changed"):
                self.run_fixture(args, lint=change_tool)
            self.assertEqual((args.report / "results.jsonl").read_text(), "")

    def test_template_change_during_evaluation_is_rejected(self):
        for filename in ("original.yaml", "normalized.json"):
            with self.subTest(filename=filename), tempfile.TemporaryDirectory() as temporary:
                args = self.fixture(Path(temporary))
                def change_input(*unused):
                    (args.corpus / filename).write_bytes(b"changed input")
                    return {"status": "valid"}
                with self.assertRaisesRegex(ValueError, "template changed"):
                    self.run_fixture(args, cue=change_input)
                self.assertEqual((args.report / "results.jsonl").read_text(), "")

    def test_guard_detects_same_size_and_restored_mtime(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "input"
            path.write_bytes(b"before")
            before = path.stat()
            guard = corpus.FileGuard([path], "changed")
            path.write_bytes(b"after!")
            # Kernels before Linux 6.13 keep timestamps at timer-tick
            # granularity, so an immediate rewrite can leave ctime unchanged.
            # Rewrite until the tick passes, as a real rebuild always would.
            # Where ctime is creation time, as on Windows, it never changes.
            deadline = time.monotonic() + 1
            while path.stat().st_ctime_ns == before.st_ctime_ns:
                if time.monotonic() > deadline:
                    self.skipTest("rewriting a file does not change its ctime here")
                time.sleep(0.001)
                path.write_bytes(b"after!")
            os.utime(path, ns=(before.st_atime_ns, before.st_mtime_ns))
            with self.assertRaisesRegex(ValueError, "changed"):
                guard.check()

    def test_guard_does_not_read_file_contents(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "input"
            path.write_bytes(b"original")
            guard = corpus.FileGuard([path], "changed")
            with mock.patch.object(Path, "open", side_effect=AssertionError("content re-read")):
                guard.check()


if __name__ == "__main__":
    unittest.main()
