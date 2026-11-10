from pathlib import Path
import re
import unittest

import check_environment

ROOT = Path(__file__).resolve().parent.parent


class PythonVersionTest(unittest.TestCase):
    def test_supported_range(self):
        self.assertIsNone(check_environment.python_problem((3, 12, 0)))
        self.assertIsNone(check_environment.python_problem((3, 14, 9)))
        self.assertIn("3.12 to 3.14", check_environment.python_problem((3, 11, 9)))
        self.assertIn("this is 3.15", check_environment.python_problem((3, 15, 2)))

    def pinned(self):
        pin = (ROOT / ".python-version").read_text().strip()
        self.assertRegex(pin, r"^3\.\d+$")
        return pin

    def test_pinned_version_is_supported(self):
        version = tuple(int(part) for part in self.pinned().split("."))
        self.assertIsNone(check_environment.python_problem(version))

    def test_dagger_image_matches_pinned_version(self):
        module = ROOT / ".dagger/modules/corpus/main.dang"
        if not module.exists():
            self.skipTest("the Dagger check does not mount its own module")
        images = re.findall(r'\.from\("python:([0-9.]+)-slim"\)', module.read_text())
        self.assertEqual(images, [self.pinned()])


if __name__ == "__main__":
    unittest.main()
