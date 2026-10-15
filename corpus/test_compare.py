"""Guard against attributing changed test inputs or budgets to schema fixes."""
import copy
import unittest

import compare


class ComparisonTests(unittest.TestCase):
    def setUp(self):
        self.report = {"summary": {"timeout_seconds": 6, "memory_soft_target": "2GiB",
                                   "validator_sha256": "old", "schema_cue_sha256": "old"},
                       "results": [{"id": "x", "region": "us-east-1", "path": "x.yaml", "url": "https://example.com/x",
                                    "lint": {"status": "valid"}, "cue": {"elapsedMs": 100},
                                    "classification": "cue-rejects-lint-accepts"}]}
        self.state = {"corpus_index_sha256": "inputs", "timeout": 6, "memory": "2GiB"}

    def test_changed_verdict_and_schema(self):
        after = copy.deepcopy(self.report)
        after["summary"]["validator_sha256"] = "new"
        after["summary"]["schema_cue_sha256"] = "new"
        after["results"][0]["classification"] = "both-accept"
        result = compare.compare(self.report, after, self.state, self.state)
        self.assertEqual(result["changes"][0]["after"], "both-accept")
        self.assertEqual(result["after"], {"both-accept": 1})

    def test_checks_are_reported(self):
        after = copy.deepcopy(self.report)
        after["summary"]["checks"] = "all"
        result = compare.compare(self.report, after, self.state, self.state)
        self.assertEqual((result["before_checks"], result["after_checks"]), ("structural", "all"))
        self.assertIn("checks structural before and all after", compare.markdown(result))

    def test_changed_inputs_or_budget_refused(self):
        for key, value in (("corpus_index_sha256", "other"), ("timeout", 15), ("memory", "4GiB")):
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, key):
                compare.compare(self.report, self.report, self.state, dict(self.state, **{key: value}))

    def test_changed_selection_refused(self):
        after = copy.deepcopy(self.report)
        after["results"][0]["id"] = "other"
        with self.assertRaisesRegex(ValueError, "same template"):
            compare.compare(self.report, after, self.state, self.state)

    def test_lint_change_refused(self):
        after = copy.deepcopy(self.report)
        after["results"][0]["lint"]["status"] = "invalid"
        with self.assertRaisesRegex(ValueError, "lint verdict changed"):
            compare.compare(self.report, after, self.state, self.state)

    def test_duplicate_refused(self):
        after = copy.deepcopy(self.report)
        after["results"].append(after["results"][0])
        with self.assertRaisesRegex(ValueError, "duplicate"):
            compare.compare(self.report, after, self.state, self.state)
