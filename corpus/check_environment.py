"""Verify the installed versions in a flat, hash-locked requirements file.

This is an offline preflight. It catches stale virtual environments and manual
package upgrades before they can change parser behavior in a recorded run.
"""
import importlib.metadata
from pathlib import Path
import re
import sys


# The upper bound is the locked cfn-lint's Requires-Python. Raise it when a
# cfn-lint upgrade does.
PYTHON_MINIMUM = (3, 12)
PYTHON_LIMIT = (3, 15)


def python_problem(version=None):
    version = tuple((version or sys.version_info)[:2])
    if PYTHON_MINIMUM <= version < PYTHON_LIMIT:
        return None
    return (
        f"Python {PYTHON_MINIMUM[0]}.{PYTHON_MINIMUM[1]} to "
        f"{PYTHON_LIMIT[0]}.{PYTHON_LIMIT[1] - 1} is required; this is "
        f"{version[0]}.{version[1]}. ./scripts/corpus.sh setup installs the "
        "version in .python-version."
    )


def check(requirements):
    problems = []
    if python_problem():
        problems.append(python_problem())
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
    if sys.argv[1:] == ["--python"]:
        sys.exit(python_problem())
    check(Path(sys.argv[1]))
