#!/usr/bin/env python3
"""Download pinned examples, normalize intrinsic syntax, and compare validators.

No template is deployed; macros, nested TemplateURLs, and source scripts are not
executed. Downloaded originals are immutable inputs, not automatic repair targets.
"""
from __future__ import annotations

import argparse
import collections
import csv
import fnmatch
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path, PurePosixPath
import re
import ssl
import subprocess
import sys
import time
import urllib.request
import zipfile

HERE = Path(__file__).resolve().parent
MAX_ARCHIVE = 64 * 1024 * 1024
MAX_TEMPLATE = 2 * 1024 * 1024


def digest(data):
    return hashlib.sha256(data).hexdigest()


def files_digest(paths):
    """Stream a stable group fingerprint without repeatedly loading binaries."""
    checksum = hashlib.sha256()
    for path in paths:
        with path.open("rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                checksum.update(block)
    return checksum.hexdigest()


class FileGuard:
    """Cheap change detection between content checks within a single run.

    Include inode, ctime and nanosecond mtime, not just size/mtime: rebuilding via
    replacement or writing the same size and restoring mtime still changes these.
    Full tool fingerprints remain verified at the beginning and end of the run.
    """
    def __init__(self, paths, message):
        self.message = message
        self.signatures = {path: self.signature(path) for path in paths}

    @staticmethod
    def signature(path):
        st = path.stat()
        return st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns, st.st_ctime_ns

    def check(self):
        for path, expected in self.signatures.items():
            try:
                actual = self.signature(path)
            except OSError as error:
                raise ValueError(self.message + ": " + str(path)) from error
            if actual != expected:
                raise ValueError(self.message + ": " + str(path))


def schema_fingerprint(root):
    checksum = hashlib.sha256()
    for path in sorted(root.rglob("*.cue")):
        checksum.update(str(path.relative_to(root)).encode() + b"\0")
        checksum.update(hashlib.sha256(path.read_bytes()).digest())
    return checksum.hexdigest()


def canonical(value):
    return (json.dumps(value, sort_keys=True, indent=2, ensure_ascii=False,
                       allow_nan=False) + "\n").encode()


def write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_bytes(data)
    temporary.replace(path)


def versions():
    return {name: importlib.metadata.version(name) for name in ["cfn-lint", "PyYAML"]}


def normalize(raw, compare_flip=False):
    from cfnlint.decode import decode_str
    from cfnlint.decode.cfn_yaml import CfnFullLoader, NodeConstructor
    # CloudFormation's format version is often an unquoted date. Preserve its
    # textual spelling rather than producing a Python-only date object.
    CfnFullLoader.add_constructor("tag:yaml.org,2002:timestamp", NodeConstructor.construct_yaml_str)
    value, errors = decode_str(raw.decode("utf-8-sig"))
    if errors:
        raise ValueError("; ".join(match.message for match in errors))
    if not isinstance(value, dict):
        raise ValueError("template is not an object")
    data = canonical(value)
    # Plain dictionaries discard the parser's location annotations, retaining
    # the original scalar types and long-form intrinsic mappings.
    value = json.loads(data)
    if compare_flip:
        from cfn_flip import to_json
        other = json.loads(to_json(raw.decode("utf-8-sig"), clean_up=False))
        if canonical(other) != data:
            raise ValueError("cfn-lint and CFN Flip normalize differently")
    return value, data


def features(value):
    intrinsics = collections.Counter()
    nodes, max_depth, intrinsic_depth = 0, 0, 0

    def visit(node, depth=0, fn_depth=0):
        nonlocal nodes, max_depth, intrinsic_depth
        nodes += 1
        max_depth = max(max_depth, depth)
        if isinstance(node, dict):
            is_function = len(node) == 1 and any(k == "Ref" or k == "Condition" or k.startswith("Fn::") for k in node)
            fn_depth += int(is_function)
            intrinsic_depth = max(intrinsic_depth, fn_depth)
            for key, child in node.items():
                if key == "Ref" or (key == "Condition" and is_function) or key.startswith("Fn::"):
                    name = "Fn::ForEach" if key.startswith("Fn::ForEach::") else key
                    intrinsics[name] += 1
                visit(child, depth + 1, fn_depth)
        elif isinstance(node, list):
            for child in node:
                visit(child, depth + 1, fn_depth)

    visit(value)
    resources = value.get("Resources", {})
    types = sorted({r.get("Type") for r in resources.values()
                    if isinstance(r, dict) and isinstance(r.get("Type"), str)}) if isinstance(resources, dict) else []
    return dict(resource_count=len(resources) if isinstance(resources, dict) else 0,
                resource_types=types, intrinsics=dict(intrinsics), nodes=nodes, depth=max_depth, intrinsic_depth=intrinsic_depth,
                transform=value.get("Transform"))


def safe_relative(path):
    posix = PurePosixPath(path)
    if posix.is_absolute() or ".." in posix.parts or "\\" in path or not posix.parts:
        raise ValueError("unsafe path: " + path)
    return posix


def fetch(args):
    lock_bytes = args.lock.read_bytes()
    lock = json.loads(lock_bytes)
    if lock.get("version") != 1:
        raise ValueError("unsupported source lock version")
    records, skipped = [], []
    for source in lock["sources"]:
        sid, repo, revision = source["id"], source["repository"], source["revision"]
        if not re.fullmatch(r"[a-z0-9-]+", sid) or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo):
            raise ValueError("invalid source identity")
        if not re.fullmatch(r"[a-f0-9]{40}", revision) or not re.fullmatch(r"[a-f0-9]{64}", source["archive_sha256"]):
            raise ValueError("sources must pin a full commit and archive SHA-256")
        archive = args.cache / (sid + ".zip")
        if not archive.exists():
            url = f"https://codeload.github.com/{repo}/zip/{revision}"
            # Standalone Python distributions on macOS may not configure a CA
            # bundle. Use the system bundle while retaining TLS verification.
            system_ca = Path("/etc/ssl/cert.pem")
            context = ssl.create_default_context(cafile=str(system_ca)) if sys.platform == "darwin" and system_ca.exists() and not os.environ.get("SSL_CERT_FILE") else None
            with urllib.request.urlopen(url, timeout=60, context=context) as response:
                data = response.read(MAX_ARCHIVE + 1)
            if len(data) > MAX_ARCHIVE:
                raise ValueError("archive exceeds download limit")
            write(archive, data)
        if archive.stat().st_size > MAX_ARCHIVE or digest(archive.read_bytes()) != source["archive_sha256"]:
            raise ValueError(f"archive checksum mismatch: {archive}; remove cache and retry")
        with zipfile.ZipFile(archive) as bundle:
            members = bundle.infolist()
            if len(members) > 25000 or sum(m.file_size for m in members) > 256 * 1024 * 1024:
                raise ValueError("archive exceeds extraction limits")
            roots = {safe_relative(m.filename).parts[0] for m in members}
            if len(roots) != 1:
                raise ValueError("expected one archive root")
            available = {str(PurePosixPath(*safe_relative(m.filename).parts[1:])) for m in members if not m.is_dir()}
            if not source.get("license_files") or any(path not in available for path in source["license_files"]):
                raise ValueError("one or more pinned license/notice files are missing")
            for member in sorted(members, key=lambda m: m.filename):
                relative = PurePosixPath(*safe_relative(member.filename).parts[1:])
                path = str(relative)
                if member.is_dir() or path == ".":
                    continue
                if ((member.external_attr >> 16) & 0o170000) == 0o120000:
                    continue
                if path in source["license_files"]:
                    if member.file_size > MAX_TEMPLATE:
                        raise ValueError("license file exceeds size limit")
                    write(args.out / "licenses" / sid / path, bundle.read(member))
                if not any(fnmatch.fnmatchcase(path, p) for p in source["include"]):
                    continue
                if any(fnmatch.fnmatchcase(path, p) for p in source.get("exclude", [])):
                    continue
                if member.file_size > MAX_TEMPLATE:
                    skipped.append(dict(source=sid, path=path, reason="template too large"))
                    continue
                raw = bundle.read(member)
                cid = digest((sid + "\0" + path).encode())[:16]
                original = Path("original") / sid / path
                normalized = Path("normalized") / (cid + ".json")
                record = dict(id=cid, source=sid, path=path,
                              url=f"https://github.com/{repo}/blob/{revision}/{path}",
                              revision=revision, license=source["license"],
                              original=str(original), original_sha256=digest(raw))
                try:
                    value, data = normalize(raw, args.compare_flip)
                    if not any(k in value for k in ("Resources", "AWSTemplateFormatVersion", "Parameters", "Transform")):
                        skipped.append(dict(source=sid, path=path, reason="not a CloudFormation template"))
                        continue
                    write(args.out / normalized, data)
                    import yaml
                    long_yaml = yaml.safe_dump(value, sort_keys=False, allow_unicode=True).encode()
                    if normalize(long_yaml)[1] != data:
                        raise ValueError("long-form YAML round trip changed template values")
                    yaml_path = normalized.with_suffix(".yaml")
                    write(args.out / yaml_path, long_yaml)
                    record.update(normalized=str(normalized), normalized_sha256=digest(data),
                                  long_yaml=str(yaml_path), long_yaml_sha256=digest(long_yaml), **features(value))
                except Exception as error:
                    record["normalization_error"] = str(error)
                write(args.out / original, raw)
                records.append(record)
        print(f"{sid}: {sum(r['source'] == sid for r in records)} templates", flush=True)
    index = dict(version=1, source_lock_sha256=digest(lock_bytes), sources=lock["sources"],
                 normalizer=versions(), templates=records, skipped=skipped)
    if args.compare_flip:
        index["normalizer"]["cfn-flip"] = importlib.metadata.version("cfn-flip")
    write(args.out / "index.json", canonical(index))
    print(f"Wrote {len(records)} templates to {args.out}; {len(skipped)} non-template/oversize files excluded")


def execute(command, timeout, env=None):
    try:
        completed = subprocess.run(command, capture_output=True, text=True, timeout=timeout, env=env)
        return completed.returncode, completed.stdout, completed.stderr
    except subprocess.TimeoutExpired:
        return 124, "", "process deadline exceeded"


def lint(path, region, timeout):
    code, stdout, stderr = execute([sys.executable, str(HERE / "lint.py"), "-t", str(path), "-r", region,
                                   "-f", "json", "--non-zero-exit-code", "error", "--mandatory-checks", "E",
                                   "--config-file", str(HERE / "cfnlintrc.json")], timeout)
    if code == 124:
        return dict(status="timeout", message=stderr)
    try:
        matches = json.loads(stdout or "[]")
        if not isinstance(matches, list):
            raise ValueError("expected a JSON array")
    except ValueError:
        return dict(status="error", message=(stderr or stdout)[:4000], exit_code=code)
    errors = [m for m in matches if m.get("Level") == "Error"]
    status = "invalid" if errors else "valid"
    if code and not errors:
        status = "error"
    return dict(status=status, matches=matches, exit_code=code, message=stderr[:4000])


def cue(args, path, region, resource=None):
    command = [str(args.validator.resolve()), "-schemas", str(args.schemas.resolve()),
               "-template", str(path.resolve()), "-region", region, "-timeout", f"{args.timeout}s"]
    if resource is not None:
        command += ["-resource", resource]
    env = os.environ.copy()
    env["GOMEMLIMIT"] = args.memory
    code, stdout, stderr = execute(command, args.timeout + 3, env)
    try:
        result = json.loads(stdout)
        if result.get("status") not in ("valid", "invalid", "unsupported", "error", "timeout"):
            raise ValueError("unexpected validator result")
        if (code == 0) != (result["status"] == "valid"):
            raise ValueError("exit status disagrees with validator result")
        return result
    except ValueError:
        return dict(status="timeout" if code == 124 else "error", message=(stderr or stdout)[:4000], exit_code=code)


# These cfn-lint errors compare a Lambda runtime's create-block (E2531) and
# update-block (E2533) dates with today's date. Schemas cannot express them,
# and the same template can pass today and fail next year.
LIFECYCLE_RULES = frozenset({"E2531", "E2533"})


def lifecycle_only(lint_result):
    errors = [m for m in lint_result.get("matches", []) if m.get("Level") == "Error"]
    return bool(errors) and all(m.get("Rule", {}).get("Id") in LIFECYCLE_RULES for m in errors)


def classify(cue_result, lint_result):
    c, l = cue_result["status"], lint_result["status"]
    if c in ("error", "timeout", "unsupported"):
        return "cue-" + c
    if l in ("error", "timeout"):
        return "lint-" + l
    if c == l == "valid":
        return "both-accept"
    if c == l == "invalid":
        return "both-reject"
    if l == "valid":
        return "cue-rejects-lint-accepts"
    if lifecycle_only(lint_result):
        return "cue-accepts-lint-lifecycle-only"
    return "cue-accepts-lint-rejects"


def choose(records, limit):
    """Deterministic greedy coverage of types, intrinsic names, and size bins."""
    if not limit or limit >= len(records):
        return sorted(records, key=lambda r: r["id"])
    candidates = {r["id"]: r for r in records}
    seen, selected = set(), []
    while candidates and len(selected) < limit:
        def tags(r):
            count = r.get("resource_count", 0)
            scale = "1" if count <= 1 else "2-5" if count <= 5 else "6-20" if count <= 20 else "21+"
            return {"t:" + t for t in r.get("resource_types", [])} | {"f:" + f for f in r.get("intrinsics", {})} | {"scale:" + scale, "source:" + r["source"]}
        best = min(candidates.values(), key=lambda r: (-len(tags(r) - seen), r["id"]))
        seen |= tags(best)
        selected.append(best)
        del candidates[best["id"]]
    return selected


def run(args):
    index_bytes = (args.corpus / "index.json").read_bytes()
    index = json.loads(index_bytes)
    manifest_bytes = (args.schemas / "manifest.json").read_bytes()
    manifest = json.loads(manifest_bytes)
    records = index["templates"]
    if args.source:
        records = [r for r in records if r["source"] in args.source.split(",")]
    if args.ids:
        wanted = set(args.ids.split(","))
        records = [r for r in records if r["id"] in wanted]
        if {r["id"] for r in records} != wanted:
            raise ValueError("one or more requested IDs were not found")
    records = choose(records, args.limit)
    if not records:
        raise ValueError("no selected templates")
    regions = sorted(manifest["regions"]) if args.regions == "all" else args.regions.split(",")
    args.regions = ",".join(regions)
    if any(region not in manifest["regions"] for region in regions):
        raise ValueError("requested region absent from schema manifest")
    expected = json.loads(args.expect.read_bytes()) if args.expect else None
    results = []
    args.report.mkdir(parents=True, exist_ok=True)
    journal = args.report / "results.jsonl"
    driver_files = [HERE / "corpus.py", HERE / "lint.py", HERE / "cfnlintrc.json"]
    tool_guard = FileGuard([args.validator, *driver_files], "validator or harness changed during the run; start a new report")
    validator_sha = files_digest([args.validator])
    driver_sha = files_digest(driver_files)
    tool_guard.check()
    schema_sha = schema_fingerprint(args.schemas)
    state = dict(corpus_index_sha256=digest(index_bytes), schema_manifest_sha256=digest(manifest_bytes), schema_cue_sha256=schema_sha,
                 validator_sha256=validator_sha, driver_sha256=driver_sha, runtime=versions(),
                 selected_ids=[r["id"] for r in records], regions=regions, timeout=args.timeout,
                 memory=args.memory, resources=args.resources, max_resource_checks=args.max_resource_checks, expected=expected)
    state_path = args.report / "run.json"
    completed = {}
    if args.resume:
        if not state_path.exists() or json.loads(state_path.read_bytes()) != state:
            raise ValueError("cannot resume: inputs, selection, tools, or run options changed")
        lines = journal.read_text().splitlines()
        valid_lines = []
        for i, line in enumerate(lines):
            try:
                row = json.loads(line)
            except ValueError:
                if i != len(lines) - 1:
                    raise ValueError("invalid checkpoint journal")
                break  # A killed writer may leave an incomplete final record.
            valid_lines.append(line)
            completed[row["id"] + ":" + row["region"]] = row
        write(journal, ("\n".join(valid_lines) + "\n").encode() if valid_lines else b"")
    else:
        write(state_path, canonical(state))
    with journal.open("a" if args.resume else "w") as output:
        for record in records:
            # Content checks happen once per template, including resumed rows.
            # Metadata guards detect changes while its regions are evaluated.
            original = args.corpus / safe_relative(record["original"])
            paths = [original]
            path = None
            if "normalized" in record:
                path = args.corpus / safe_relative(record["normalized"])
                paths.append(path)
            input_guard = FileGuard(paths, "template changed during region evaluation")
            if files_digest([original]) != record["original_sha256"]:
                raise ValueError("original template checksum mismatch: " + str(original))
            normalized_bytes = path.read_bytes() if path else None
            if path and digest(normalized_bytes) != record["normalized_sha256"]:
                raise ValueError("normalized template checksum mismatch: " + str(path))
            input_guard.check()
            for region in regions:
                row = dict(id=record["id"], source=record["source"], path=record["path"], url=record["url"],
                           region=region, resource_count=record.get("resource_count", 0),
                           resource_types=record.get("resource_types", []), intrinsics=record.get("intrinsics", {}))
                key = record["id"] + ":" + region
                if key in completed:
                    results.append(completed[key])
                    continue
                tool_guard.check()
                input_guard.check()
                row["lint"] = lint(original.resolve(), region, args.timeout)
                if "normalization_error" in record:
                    row["cue"] = dict(status="error", message=record["normalization_error"])
                    row["classification"] = "normalization-error"
                else:
                    tool_guard.check()
                    input_guard.check()
                    row["cue"] = cue(args, path, region)
                    row["classification"] = classify(row["cue"], row["lint"])
                    row["resources"] = {}
                    if args.resources == "all" or (args.resources == "failures" and row["cue"]["status"] != "valid"):
                        value = json.loads(normalized_bytes)
                        resources = value.get("Resources", {})
                        if isinstance(resources, dict):
                            names = sorted(resources)
                            if args.max_resource_checks and len(names) > args.max_resource_checks:
                                row["resource_checks_skipped"] = names[args.max_resource_checks:]
                                names = names[:args.max_resource_checks]
                            for name in names:
                                tool_guard.check()
                                input_guard.check()
                                row["resources"][name] = cue(args, path, region, name)
                tool_guard.check()
                input_guard.check()
                if expected is not None:
                    expectation = expected.get(record["id"] + ":" + region)
                    row["contract_pass"] = bool(expectation and
                        all(row[engine]["status"] == expectation[engine] for engine in ("cue", "lint")))
                results.append(row)
                output.write(json.dumps(row, ensure_ascii=False) + "\n")
                output.flush()
                print(f"{len(results)}/{len(records)*len(regions)} {record['source']} {record['path']} [{region}]: {row['classification']}", flush=True)
    tool_guard.check()
    if files_digest([args.validator]) != validator_sha or files_digest(driver_files) != driver_sha:
        raise ValueError("validator or harness changed during the run; start a new report")
    tool_guard.check()
    summary = summarize(results, index, manifest, digest(manifest_bytes), args)
    summary["schema_cue_sha256"] = schema_sha
    summary["validator_sha256"] = validator_sha
    summary["driver_sha256"] = driver_sha
    write(args.report / "report.json", canonical(dict(summary=summary, results=results)))
    markdown(args.report / "report.md", summary, results)
    with (args.report / "resources.csv").open("w", newline="") as output:
        writer = csv.writer(output)
        writer.writerow(["template_id", "path", "region", "logical_id", "resource_type", "status", "elapsed_ms", "heap_mib"])
        for row in results:
            for name, result in row.get("resources", {}).items():
                writer.writerow([row["id"], row["path"], row["region"], name,
                                 ";".join(result.get("resourceTypes", [])), result["status"],
                                 result.get("elapsedMs"), result.get("heapMiB")])
    print(json.dumps(summary["classifications"], sort_keys=True))
    if expected is not None:
        selected_keys = {r["id"] + ":" + r["region"] for r in results}
        if not all(r["contract_pass"] for r in results) or set(expected) != selected_keys:
            raise SystemExit("corpus contract changed, missing, or contains unexecuted cases; inspect report")
    if args.fail_on_disagreement and any(r["classification"] != "both-accept" for r in results):
        raise SystemExit("corpus contains rejections, disagreements, unsupported inputs, or timeouts; inspect report")


def summarize(results, index, manifest, sha, args):
    types = sorted({typ for row in results for typ in row["resource_types"]})
    accepted = sorted({typ for row in results if row["cue"]["status"] == "valid" for typ in row["resource_types"]})
    intrinsic_names = sorted({fn for row in results for fn in row["intrinsics"]})
    scales = {}
    for row in results:
        count = row["resource_count"]
        label = "1" if count <= 1 else "2-5" if count <= 5 else "6-20" if count <= 20 else "21+"
        scales.setdefault(label, collections.Counter())[row["classification"]] += 1
    return dict(templates=len({r["id"] for r in results}), evaluations=len(results), scale_results=scales,
                downloaded_templates=len(index["templates"]), regions=args.regions.split(","),
                classifications=dict(collections.Counter(r["classification"] for r in results)),
                unique_resource_types=len(types), resource_types=types, accepted_resource_types=accepted,
                intrinsics=intrinsic_names, schema_manifest_sha256=sha, schema_source_sha256=manifest["sourceSHA256"],
                normalizer=index["normalizer"], lint_runtime=versions(), source_lock_sha256=index["source_lock_sha256"],
                timeout_seconds=args.timeout, memory_soft_target=args.memory,
                resource_checks=sum(len(r.get("resources", {})) for r in results),
                resource_checks_skipped=sum(len(r.get("resource_checks_skipped", [])) for r in results),
                sources=index.get("sources", []))


def markdown(path, summary, results):
    lines = ["# CloudFormation corpus validation", "", f"{summary['templates']} templates; {summary['evaluations']} region evaluations; {summary['unique_resource_types']} resource types encountered; {len(summary['accepted_resource_types'])} types in CUE-accepted templates; {summary['resource_checks']} individual resource checks ({summary['resource_checks_skipped']} omitted by the resource check cap).", "",
             "Original YAML was compared with cfn-lint. CUE received JSON with expanded intrinsic tags and unchanged scalar types. No stacks were deployed or custom macros executed.", "",
             f"CUE process deadline: {summary['timeout_seconds']}s. GOMEMLIMIT soft target: {summary['memory_soft_target']} (not a hard memory limit or a peak measurement).", "",
             "| Result | Evaluations |", "| --- | ---: |"]
    lines += [f"| {key} | {count} |" for key, count in sorted(summary["classifications"].items())]
    lines += ["", "## Scale", "", "| Resources per template | Accepted by both | CUE timeouts | Other results |", "| --- | ---: | ---: | ---: |"]
    for label in ("1", "2-5", "6-20", "21+"):
        counts = summary["scale_results"].get(label, {})
        lines.append(f"| {label} | {counts.get('both-accept', 0)} | {counts.get('cue-timeout', 0)} | {sum(counts.values()) - counts.get('both-accept', 0) - counts.get('cue-timeout', 0)} |")
    lines += ["", "Disagreements require triage: cfn-lint acceptance is a comparison signal, not proof of deployability. CUE checks structure and intrinsic argument/result classes; reference resolution, conditions, macros, and service behavior require semantic validation.", "",
              "## Cases requiring attention", "", "| Source template | Region | Result | CUE time (ms) |", "| --- | --- | --- | ---: |"]
    lines += [f"| [{r['path']}]({r['url']}) | {r['region']} | {r['classification']} | {r['cue'].get('elapsedMs', '')} |" for r in results
              if r["classification"] not in ("both-accept", "cue-accepts-lint-lifecycle-only")]
    lifecycle = summary["classifications"].get("cue-accepts-lint-lifecycle-only", 0)
    if lifecycle:
        lines += ["", f"{lifecycle} evaluations are omitted above because cfn-lint's only errors were Lambda runtime lifecycle dates (E2531, E2533)."]
    lines += ["", "Full diagnostics, source hashes, tool versions, intrinsic coverage, and accepted resource coverage are in report.json; isolated resource timings are in resources.csv.", ""]
    write(path, "\n".join(lines).encode())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    p = commands.add_parser("fetch", help="fetch immutable source archives and normalize YAML")
    p.add_argument("--lock", type=Path, default=HERE / "sources.lock.json")
    p.add_argument("--out", type=Path, default=Path(".corpus"))
    p.add_argument("--cache", type=Path, default=Path(".corpus-cache"))
    p.add_argument("--compare-flip", action="store_true", help="require equivalence with pinned optional CFN Flip")
    p.set_defaults(func=fetch)
    p = commands.add_parser("run", help="compare CUE and cfn-lint with bounded per-input processes")
    p.add_argument("--corpus", type=Path, default=Path(".corpus"))
    p.add_argument("--schemas", type=Path, default=Path("generated"))
    p.add_argument("--validator", type=Path, default=Path("bin/cfn-cue-validate"))
    p.add_argument("--report", type=Path, default=Path(".corpus-report"))
    p.add_argument("--regions", default="us-east-1")
    p.add_argument("--source", help="comma-separated source IDs")
    p.add_argument("--ids", help="comma-separated template IDs")
    p.add_argument("--limit", type=int, default=50, help="deterministic coverage sample; 0 checks all")
    p.add_argument("--timeout", type=float, default=30)
    p.add_argument("--memory", default="2GiB", help="Go memory soft target, not a hard limit")
    p.add_argument("--resources", choices=["none", "failures", "all"], default="failures")
    p.add_argument("--max-resource-checks", type=int, default=10, help="per-template resource check cap; 0 checks every resource")
    p.add_argument("--expect", type=Path, help="exact expected CUE and lint statuses for every selected case/region")
    p.add_argument("--fail-on-disagreement", action="store_true")
    p.add_argument("--resume", action="store_true", help="resume a journal only when all inputs, tools, and options match")
    p.set_defaults(func=run)
    args = parser.parse_args()
    if args.command == "run" and (args.limit < 0 or args.max_resource_checks < 0 or args.timeout <= 0):
        parser.error("limits must be nonnegative and timeout must be positive")
    args.func(args)


if __name__ == "__main__":
    main()
