import argparse
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

import corpus
from lint import CorpusConfig


class NormalizationTests(unittest.TestCase):
    def test_intrinsics_and_scalar_types(self):
        value, normalized = corpus.normalize(b"""AWSTemplateFormatVersion: 2010-09-09
Resources:
  Bucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub '${Name}-bucket'
      Tags:
        - Key: Name
          Value: !Join ['-', [!Ref Name, !GetAtt Other.Name]]
Metadata:
  QuotedNumber: '2'
  Number: 2
  Boolean: true
  QuotedBoolean: 'false'
""")
        self.assertEqual(value["AWSTemplateFormatVersion"], "2010-09-09")
        tags = value["Resources"]["Bucket"]["Properties"]["Tags"]
        self.assertEqual(tags[0]["Value"], {"Fn::Join": ["-", [{"Ref": "Name"}, {"Fn::GetAtt": ["Other", "Name"]}]]})
        self.assertEqual(value["Metadata"], {"QuotedNumber": "2", "Number": 2, "Boolean": True, "QuotedBoolean": "false"})
        self.assertEqual(json.loads(normalized), value)
        self.assertEqual(corpus.features(value)["intrinsics"]["Fn::Join"], 1)

    def test_duplicate_keys_are_errors(self):
        with self.assertRaises(ValueError):
            corpus.normalize(b"Resources:\n  Bucket: {}\n  Bucket: {}\n")

    def test_long_form_round_trip(self):
        raw = b'{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":{"Fn::If":["Create","name",{"Ref":"AWS::NoValue"}]}}}}}'
        value, normalized = corpus.normalize(raw)
        self.assertEqual(value, json.loads(raw))
        self.assertEqual(normalized, corpus.canonical(json.loads(raw)))


class CorpusTests(unittest.TestCase):
    def test_source_linter_configuration_is_ignored(self):
        config = CorpusConfig(["--config-file", str(corpus.HERE / "cfnlintrc.json"), "--mandatory-checks", "E"])
        config.template_args = {"Metadata": {"cfn-lint": {"config": {
            "append_rules": ["untrusted.py"], "ignore_checks": ["E"], "override_spec": "untrusted.json"}}}}
        self.assertNotIn("untrusted.py", config.append_rules)
        self.assertFalse(config.ignore_checks)
        self.assertFalse(config.override_spec)
        self.assertEqual(config.mandatory_checks, ["E"])

    def test_fetch_verifies_archive_and_preserves_original(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            raw = b"Resources:\n  Bucket:\n    Type: AWS::S3::Bucket\n"
            cache = root / "cache"
            cache.mkdir()
            archive = cache / "example.zip"
            with zipfile.ZipFile(archive, "w") as bundle:
                bundle.writestr("repository-commit/sample.yaml", raw)
                bundle.writestr("repository-commit/LICENSE", "license fixture")
            lock = root / "sources.json"
            lock.write_bytes(corpus.canonical({"version": 1, "sources": [{
                "id": "example", "repository": "owner/repository", "revision": "a" * 40,
                "archive_sha256": corpus.digest(archive.read_bytes()), "license": "test",
                "include": ["*.yaml"], "license_files": ["LICENSE"]}]}))
            args = argparse.Namespace(lock=lock, cache=cache, out=root / "out", compare_flip=False)
            with contextlib.redirect_stdout(io.StringIO()):
                corpus.fetch(args)
            index = json.loads((args.out / "index.json").read_bytes())
            record = index["templates"][0]
            self.assertEqual((args.out / record["original"]).read_bytes(), raw)
            self.assertEqual(corpus.digest(raw), record["original_sha256"])
            self.assertTrue((args.out / "licenses/example/LICENSE").exists())
            archive.write_bytes(b"changed")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                corpus.fetch(args)

    def test_unsafe_paths(self):
        for path in ("../sample", "/sample", "a/../../sample", "a\\sample"):
            with self.assertRaises(ValueError):
                corpus.safe_relative(path)

    def test_sampling_is_deterministic_and_expands_coverage(self):
        records = [{"id": "a", "source": "one", "resource_types": ["A"], "intrinsics": {"Ref": 1}},
                   {"id": "b", "source": "one", "resource_types": ["A", "B"], "intrinsics": {"Fn::Sub": 1}},
                   {"id": "c", "source": "two", "resource_types": ["C"], "intrinsics": {"Fn::GetAtt": 1}}]
        chosen = corpus.choose(records, 2)
        self.assertEqual(chosen, corpus.choose(list(reversed(records)), 2))
        self.assertEqual({r["id"] for r in chosen}, {"b", "c"})

    def test_disagreement_is_not_a_pass(self):
        self.assertEqual(corpus.classify({"status": "invalid"}, {"status": "valid"}), "cue-rejects-lint-accepts")
        self.assertEqual(corpus.classify({"status": "valid"}, {"status": "invalid"}), "cue-accepts-lint-rejects")
        self.assertEqual(corpus.classify({"status": "timeout"}, {"status": "valid"}), "cue-timeout")

    def test_lifecycle_only_lint_errors_are_separate(self):
        def lint(*rules):
            return {"status": "invalid", "matches": [{"Level": level, "Rule": {"Id": rule}} for level, rule in rules]}
        runtime = lint(("Error", "E2533"), ("Error", "E2531"), ("Warning", "W2531"))
        self.assertEqual(corpus.classify({"status": "valid"}, runtime), "cue-accepts-lint-lifecycle-only")
        mixed = lint(("Error", "E2533"), ("Error", "E3045"))
        self.assertEqual(corpus.classify({"status": "valid"}, mixed), "cue-accepts-lint-rejects")
        # CUE rejecting the template is still a shared rejection.
        self.assertEqual(corpus.classify({"status": "invalid"}, runtime), "both-reject")


if __name__ == "__main__":
    unittest.main()
