#!/usr/bin/env python3
"""Paired cfn-cue-validate timings of corpus templates against two schema builds.

Each round runs every selected case once per build, in an order shuffled with a
fixed seed, so machine load affects both builds alike. Values are the
validator's elapsedMs. The first build is the baseline.

    scripts/paired_timings.py --types AWS::DynamoDB::Table \\
        --out .paired.json before=/tmp/before after=generated
"""
import argparse
import json
import os
from pathlib import Path
import random
import statistics
import subprocess
import sys

REPO = Path(__file__).resolve().parents[1]


def cases(corpus, types, per_resource):
    """Normalized templates, or their resources, that use one of types."""
    selected = []
    for path in sorted((corpus / "normalized").glob("*.json")):
        try:
            template = json.loads(path.read_text())
        except ValueError:
            continue
        resources = template.get("Resources") if isinstance(template, dict) else None
        if not isinstance(resources, dict):
            continue
        names = sorted(name for name, resource in resources.items()
                       if isinstance(resource, dict) and isinstance(resource.get("Type"), str) and resource["Type"] in types)
        if per_resource:
            selected += [{"path": str(path.relative_to(corpus)), "resource": name} for name in names]
        elif names:
            selected.append({"path": str(path.relative_to(corpus))})
    return selected


def run(validator, schemas, corpus, case, region, timeout, memory):
    command = [str(validator), "-schemas", str(schemas), "-region", region,
               "-template", str(corpus / case["path"]), "-timeout", f"{timeout}s"]
    if "resource" in case:
        command += ["-resource", case["resource"]]
    environment = dict(os.environ, GOMEMLIMIT=memory)
    output = subprocess.run(command, capture_output=True, text=True, env=environment).stdout
    result = json.loads(output)
    return result.get("elapsedMs"), result["status"]


def summarize(rows, builds):
    by_case = {}
    for row in rows:
        by_case.setdefault((row["path"], row.get("resource")), []).append(row)
    medians = {build: {key: statistics.median(r[build] for r in group)
                       for key, group in by_case.items()} for build in builds}
    base = builds[0]
    summary = {"cases": len(by_case), "rounds": max(r["rep"] for r in rows) + 1, "builds": {}}
    for build in builds:
        values = medians[build]
        entry = {"median_ms": statistics.median(values.values()), "total_ms": sum(values.values())}
        if build != base:
            difference = [values[key] - medians[base][key] for key in values]
            paired = [r[build] - r[base] for r in rows]
            entry.update({
                "median_case_increase_ms": statistics.median(difference),
                "median_paired_difference_ms": statistics.median(paired),
                "max_case_increase_ms": max(difference),
                "total_increase_percent": round(100 * (entry["total_ms"] / summary["builds"][base]["total_ms"] - 1), 1),
            })
        summary["builds"][build] = entry
    summary["status_changes"] = [
        {"path": key[0], "resource": key[1], **{b: group[0][b + "_status"] for b in builds}}
        for key, group in sorted(by_case.items(), key=lambda kv: (kv[0][0], kv[0][1] or ""))
        if len({group[0][b + "_status"] for b in builds}) > 1]
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("builds", nargs=2, metavar="NAME=SCHEMAS", help="baseline first")
    parser.add_argument("--types", required=True, help="comma-separated resource types")
    parser.add_argument("--resources", action="store_true", help="time each resource with -resource")
    parser.add_argument("--corpus", type=Path, default=REPO / ".corpus")
    parser.add_argument("--validator", type=Path, default=REPO / "bin/cfn-cue-validate")
    parser.add_argument("--region", default="us-east-1")
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--seed", type=int, default=7)
    parser.add_argument("--timeout", type=float, default=30)
    parser.add_argument("--memory", default="2GiB")
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    builds = dict(build.split("=", 1) for build in args.builds)
    names = list(builds)
    selected = cases(args.corpus, set(args.types.split(",")), args.resources)
    if not selected:
        sys.exit("no corpus cases use the selected types")
    rng = random.Random(args.seed)
    rows = []
    for rep in range(args.rounds):
        jobs = [(i, name) for i in range(len(selected)) for name in names]
        rng.shuffle(jobs)
        results = {}
        for i, name in jobs:
            elapsed, status = run(args.validator, builds[name], args.corpus, selected[i],
                                  args.region, args.timeout, args.memory)
            row = results.setdefault(i, dict(selected[i], rep=rep))
            row[name], row[name + "_status"] = elapsed, status
        rows += [results[i] for i in sorted(results)]
        print(f"round {rep + 1}/{args.rounds}: {len(selected)} cases", file=sys.stderr)
    summary = summarize(rows, names)
    summary.update({"types": sorted(set(args.types.split(","))), "seed": args.seed,
                    "timeout_s": args.timeout, "memory": args.memory, "region": args.region})
    args.out.write_text(json.dumps({"summary": summary, "rows": rows}, indent=1) + "\n")
    print(json.dumps(summary, indent=1))


if __name__ == "__main__":
    main()
