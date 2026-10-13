"""Verify the installed versions in a flat, hash-locked requirements file.

This is an offline preflight. It catches stale virtual environments and manual
package upgrades before they can change parser behavior in a recorded run.
"""
import importlib.metadata
from pathlib import Path
import re
import sys


def check(requirements):
    problems = []
    if sys.version_info < (3, 12):
        problems.append("Python 3.12 or newer is required")
    pins = []
    for line in requirements.read_text().splitlines():
        if not line or line[0].isspace() or line.startswith("#"):
            continue
        match = re.fullmatch(r"([A-Za-z0-9_.-]+)==([^\s;]+)(?:\s+\\)?", line)
        if not match:
            raise SystemExit("Expected a flat hash-locked package pin: " + line)
        pins.append(match.groups())
    if not pins:
        raise SystemExit("No package pins found: " + str(requirements))
    for name, expected in pins:
        try:
            actual = importlib.metadata.version(name)
        except importlib.metadata.PackageNotFoundError:
            actual = "missing"
        if actual != expected:
            problems.append(f"{name}: expected {expected}, installed {actual}")
    if problems:
        command = "setup-flip" if requirements.name == "requirements-flip.txt" else "setup"
        raise SystemExit("\n".join(problems) + f"\nRun ./scripts/corpus.sh {command} to restore the locked environment.")


if __name__ == "__main__":
    check(Path(sys.argv[1]))
