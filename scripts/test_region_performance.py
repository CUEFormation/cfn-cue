import os
from pathlib import Path
import sys
import tempfile
import unittest

from region_performance import measure, ordered_types


class PerformanceHarnessTests(unittest.TestCase):
    def test_nested_samples_keep_needed_types(self):
        types = {"AWS::S3::Bucket", "AWS::IAM::Role", "AWS::A::Thing", "AWS::B::Thing"}
        order = ordered_types(types)
        self.assertEqual(order[:2], ["AWS::S3::Bucket", "AWS::IAM::Role"])
        self.assertEqual(order, ordered_types(reversed(sorted(types))))
        self.assertEqual(ordered_types(types, {"AWS::B::Thing"})[0], "AWS::B::Thing")
        with self.assertRaises(ValueError):
            ordered_types(types, {"AWS::Unavailable::Thing"})

    def run_child(self, code, timeout):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            return measure([sys.executable, "-c", code], root, timeout, os.environ,
                           root / "stdout", root / "stderr")

    def test_completed_process_and_diagnostics(self):
        result = self.run_child("import sys; print('out'); print('err', file=sys.stderr); sys.exit(3)", 3)
        self.assertFalse(result["timeout"])
        self.assertEqual(result["exitCode"], 3)
        self.assertEqual(result["stdoutBytes"], 4)
        self.assertEqual(result["stderrBytes"], 4)
        if sys.platform in ("darwin", "linux"):
            self.assertGreater(result["peakRSSBytes"], 0)

    def test_deadline_kills_and_reaps_process(self):
        result = self.run_child("import time; time.sleep(10)", 0.1)
        self.assertTrue(result["timeout"])
        self.assertNotEqual(result["exitCode"], 0)
        self.assertLess(result["elapsedSeconds"], 3)


if __name__ == "__main__":
    unittest.main()
