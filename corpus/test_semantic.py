"""The checked-in semantic corpus must match its generator."""
import json
from pathlib import Path
import tempfile
import unittest

import corpus
import make_semantic


class SemanticCorpusTests(unittest.TestCase):
    def test_checked_in_cases_match_generator(self):
        checked_in = corpus.HERE / "semantic"
        with tempfile.TemporaryDirectory() as temporary:
            out = Path(temporary)
            make_semantic.build(out)
            generated = sorted(p.relative_to(out) for p in out.rglob("*") if p.is_file())
            existing = sorted(p.relative_to(checked_in) for p in checked_in.rglob("*") if p.is_file())
            self.assertEqual(generated, existing)
            for path in generated:
                self.assertEqual((out / path).read_bytes(), (checked_in / path).read_bytes(), str(path))

    def test_every_case_has_valid_and_invalid_coverage(self):
        expected = json.loads((corpus.HERE / "semantic" / "expectations.json").read_bytes())
        statuses = {value["cue"] for value in expected.values()}
        self.assertEqual(statuses, {"valid", "invalid"})
        for key, value in expected.items():
            if value["cue"] != value["lint"]:
                self.assertIn("Known divergence", value["note"], key)


if __name__ == "__main__":
    unittest.main()
