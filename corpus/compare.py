"""Compare two corpus runs with the same inputs, lint runtime, and budgets."""
import argparse
import collections
import json
from pathlib import Path


def load(directory):
    directory = Path(directory)
    return (json.loads((directory / "report.json").read_bytes()),
            json.loads((directory / "run.json").read_bytes()))


def compare(before, after, before_state, after_state):
    # Validator and generated schema fingerprints should change. Inputs and
    # comparison settings should not, so results remain attributable.
    for key in ("corpus_index_sha256", "driver_sha256", "runtime", "regions",
                "timeout", "memory", "resources", "max_resource_checks"):
        if before_state.get(key) != after_state.get(key):
            raise ValueError("runs differ in " + key)
    for key in ("schema_source_sha256", "source_lock_sha256", "normalizer", "lint_runtime"):
        if before["summary"].get(key) != after["summary"].get(key):
            raise ValueError("runs differ in " + key)

    def keyed(report):
        rows = {(row["id"], row["region"]): row for row in report["results"]}
        if len(rows) != len(report["results"]):
            raise ValueError("duplicate template/region result")
        return rows

    old, new = keyed(before), keyed(after)
    if old.keys() != new.keys():
        raise ValueError("runs do not contain the same template/region cases")
    changes, transitions = [], collections.Counter()
    for key in sorted(old):
        a, b = old[key], new[key]
        if a["lint"]["status"] != b["lint"]["status"]:
            raise ValueError("lint verdict changed for " + ":".join(key))
        transitions[(a["classification"], b["classification"])] += 1
        if a["classification"] != b["classification"]:
            changes.append(dict(id=key[0], region=key[1], path=b["path"], url=b["url"],
                                before=a["classification"], after=b["classification"],
                                before_cue_ms=a["cue"].get("elapsedMs"),
                                after_cue_ms=b["cue"].get("elapsedMs")))

    def counts(rows):
        return dict(sorted(collections.Counter(r["classification"] for r in rows.values()).items()))

    return dict(evaluations=len(old), before=counts(old), after=counts(new), changes=changes,
                transitions=[dict(before=a, after=b, count=n)
                             for (a, b), n in sorted(transitions.items())],
                timeout_seconds=after["summary"]["timeout_seconds"],
                before_checks=before["summary"].get("checks", "structural"),
                after_checks=after["summary"].get("checks", "structural"),
                memory_soft_target=after["summary"]["memory_soft_target"],
                before_validator_sha256=before["summary"]["validator_sha256"],
                after_validator_sha256=after["summary"]["validator_sha256"],
                before_schema_sha256=before["summary"]["schema_cue_sha256"],
                after_schema_sha256=after["summary"]["schema_cue_sha256"])


def markdown(result):
    lines = ["# Corpus comparison", "",
             f"{result['evaluations']} identical template/region cases; "
             f"{result['timeout_seconds']}s deadline; "
             f"{result['memory_soft_target']} memory soft target; "
             f"checks {result['before_checks']} before and {result['after_checks']} after.", "",
             "Input checksums, schema source, harness, lint runtime, and run settings match. "
             "Machine load can still affect timing and timeout outcomes.", "",
             "| Result | Before | After |", "| --- | ---: | ---: |"]
    for label in sorted(result["before"].keys() | result["after"].keys()):
        lines.append(f"| {label} | {result['before'].get(label, 0)} | {result['after'].get(label, 0)} |")
    lines += ["", "## Changed outcomes", "",
              "| Template | Region | Before | After |", "| --- | --- | --- | --- |"]
    for row in result["changes"]:
        lines.append(f"| [{row['path']}]({row['url']}) | {row['region']} | {row['before']} | {row['after']} |")
    lines += ["", "Timeouts and unsupported inputs remain unresolved. Agreement with cfn-lint "
              "does not establish deployability. Exact fingerprints and all transitions are in comparison.json.", ""]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path, help="directory containing report.json and run.json")
    parser.add_argument("after", type=Path, help="directory containing report.json and run.json")
    parser.add_argument("--out", type=Path, default=Path(".corpus-comparison"))
    args = parser.parse_args()
    try:
        before, old_state = load(args.before)
        after, new_state = load(args.after)
        result = compare(before, after, old_state, new_state)
    except (ValueError, OSError, KeyError) as error:
        parser.error(str(error))
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / "comparison.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    (args.out / "comparison.md").write_text(markdown(result))
    print(f"Compared {result['evaluations']} cases; {len(result['changes'])} changed outcomes. Read {args.out / 'comparison.md'}")


if __name__ == "__main__":
    main()
