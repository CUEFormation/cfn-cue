#!/usr/bin/env python3
"""Bounded, paired CUE CLI measurements of full and type-name regional schemas."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import shutil
import signal
import subprocess
import sys
import time

REPO = Path(__file__).resolve().parents[1]


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def schema_fingerprint(directory):
    fingerprint = hashlib.sha256()
    for path in sorted(directory.rglob("*.cue")):
        fingerprint.update(str(path.relative_to(directory)).encode() + b"\0" + path.read_bytes())
    return fingerprint.hexdigest()


def region_types(manifest, region):
    """The region's type-to-schema map, read from the manifest's types block."""
    if region not in manifest["regions"]:
        raise ValueError("region absent from the schema manifest: " + region)
    types = {}
    for typ, info in manifest["types"].items():
        if info.get("regions", "*") == "*":
            present = region not in info.get("exceptRegions", ())
        else:
            present = region in info["regions"]
        if present:
            types[typ] = info.get("hashes", {}).get(region, info["hash"])
    return types


def ordered_types(types, required=()):
    """Nested samples, with needed types first and a stable hash order after."""
    types = set(types)
    first = sorted(set(required)) if required else [
        t for t in ("AWS::S3::Bucket", "AWS::IAM::Role") if t in types]
    if set(first) - set(types):
        raise ValueError("template uses resource types unavailable in the selected region")
    rest = sorted(set(types) - set(first), key=lambda t: hashlib.sha256(t.encode()).hexdigest())
    return first + rest


def measure(command, directory, timeout, environment, stdout, stderr):
    """Capture per-child RSS on Linux/macOS, including processes killed at deadline."""
    started = time.monotonic()
    usage = None
    timed_out = False
    with stdout.open("wb") as output, stderr.open("wb") as errors:
        process = subprocess.Popen(command, cwd=directory, env=environment,
                                   stdout=output, stderr=errors)
        try:
            while True:
                if hasattr(os, "wait4"):
                    pid, status, observed = os.wait4(process.pid, os.WNOHANG)
                    if pid:
                        process.returncode = os.waitstatus_to_exitcode(status)
                        usage = observed
                        break
                elif process.poll() is not None:
                    break
                if time.monotonic() - started >= timeout:
                    timed_out = True
                    if hasattr(os, "wait4"):
                        # Popen.kill() can reap an already-exited child internally,
                        # which would lose the per-process wait4 usage record.
                        try:
                            os.kill(process.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        _, status, usage = os.wait4(process.pid, 0)
                        process.returncode = os.waitstatus_to_exitcode(status)
                    else:
                        process.kill()
                        process.wait()
                    break
                time.sleep(0.02)
        finally:
            if process.returncode is None:
                process.kill()
                process.wait()
    rss = None
    if usage is not None and sys.platform in ("darwin", "linux"):
        rss = int(usage.ru_maxrss * (1 if sys.platform == "darwin" else 1024))
    return dict(elapsedSeconds=round(time.monotonic() - started, 4), timeout=timed_out,
                exitCode=process.returncode, peakRSSBytes=rss,
                userCPUSeconds=usage.ru_utime if usage else None,
                systemCPUSeconds=usage.ru_stime if usage else None,
                stdoutBytes=stdout.stat().st_size, stderrBytes=stderr.stat().st_size)


def executable(value):
    found = shutil.which(value)
    if found:
        return str(Path(found).resolve())
    raise ValueError("executable not found: " + value)


def write_report(out, metadata, observations):
    (out / "report.json").write_text(json.dumps(dict(metadata=metadata, observations=observations), indent=2) + "\n")
    lines = ["# Regional CUE performance", "", f"Region: `{metadata['region']}`. Source: `{metadata['sourceSHA256']}`.",
             f"Platform: `{metadata['platform']}`. Deadline: {metadata['timeoutSeconds']}s per command. Go memory target: `{metadata['memoryTarget']}`.",
             "", "```text", metadata["cueVersion"].strip(), "```", "",
             "| Types | Representation | Operation | Repeat | Status | Seconds | Peak RSS MiB |",
             "| ---: | --- | --- | ---: | --- | ---: | ---: |"]
    for item in observations:
        elapsed = (">=" + str(metadata["timeoutSeconds"])) if item["status"] == "timeout" else str(item["elapsedSeconds"])
        rss = str(round(item["peakRSSBytes"] / (1024 * 1024), 1)) if item["peakRSSBytes"] is not None else "unavailable"
        lines.append(f"| {item['resourceCount']} | {item['representation']} | {item['operation']} | {item['repeat']} | {item['status']} | {elapsed} | {rss} |")
    lines += ["", "Timings include the CLI's loading, evaluation and output formatting; schema generation is excluded.",
              "Timeouts are incomplete observations, not completed evaluation times. Peak RSS is per CLI process on Linux/macOS and includes timed-out processes.",
              "GOMEMLIMIT is a soft Go memory target, not a hard process limit. Each measurement starts a fresh CLI process; operating-system file caches are not cleared.",
              "Type-name validation leaves resource Properties unconstrained. Compare acceptance only with that difference in scope understood.",
              "Use report.json to compare exact selected type lists, schema and executable fingerprints, template checksums, platforms, budgets and individual repeats."]
    (out / "report.md").write_text("\n".join(lines) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schemas", type=Path, default=REPO / "generated")
    parser.add_argument("--source", help="schema bundle for filtered scales; defaults to the manifest source")
    parser.add_argument("--generator", default=str(REPO / "bin/cfn-cue"))
    parser.add_argument("--cue", default="cue")
    parser.add_argument("--region", default="us-east-1")
    parser.add_argument("--sizes", default="2,10,50,all")
    parser.add_argument("--repeats", type=int, default=1)
    parser.add_argument("--timeout", type=float, default=20)
    parser.add_argument("--memory", default="2GiB")
    parser.add_argument("--template", type=Path, help="optional normalized long-form YAML or JSON")
    parser.add_argument("--out", type=Path, default=REPO / ".region-performance-report")
    args = parser.parse_args()
    if not math.isfinite(args.timeout) or args.timeout <= 0 or args.repeats < 1:
        parser.error("--timeout and --repeats must be positive")
    try:
        schemas = args.schemas.resolve()
        manifest = json.loads((schemas / "manifest.json").read_text())
        types = region_types(manifest, args.region)
        cue = executable(args.cue)
        sizes = sorted(set(len(types) if n == "all" else int(n) for n in args.sizes.split(",")))
        if not sizes or min(sizes) < 1 or max(sizes) > len(types):
            raise ValueError("sizes must be between 1 and the regional resource count")
        if not (schemas / "regions" / args.region / "types/region.cue").is_file():
            raise ValueError("regenerate schemas first: the regional types package is missing")
        required = set()
        template = args.template.resolve() if args.template else None
        if template:
            parsed = subprocess.run([cue, "export", str(template), "--out", "json"],
                                    cwd=REPO, capture_output=True, text=True, timeout=args.timeout, check=True)
            data = json.loads(parsed.stdout)
            for resource in data.get("Resources", {}).values():
                typ = resource["Type"]
                required.add("AWS::CloudFormation::CustomResource" if typ.startswith("Custom::") else typ)
        order = ordered_types(types, required)
        if required and min(sizes) < len(required):
            raise ValueError("every size must include all " + str(len(required)) + " resource types used by the template")
        if args.out.exists() and any(args.out.iterdir()):
            raise ValueError("--out must be empty; use a new directory to preserve previous observations")
        out = args.out.resolve()
        (out / "logs").mkdir(parents=True, exist_ok=True)
        version = subprocess.check_output([cue, "version"], text=True, timeout=args.timeout)
        metadata = dict(region=args.region, sourceSHA256=manifest["sourceSHA256"],
                        schemaFingerprint=schema_fingerprint(schemas), cueVersion=version,
                        cueExecutableSHA256=sha(Path(cue)), platform=platform.platform(),
                        machine=platform.machine(), pythonVersion=platform.python_version(),
                        timeoutSeconds=args.timeout, memoryTarget=args.memory, repeats=args.repeats,
                        templateSHA256=sha(template) if template else None, selections={},
                        moduleFingerprints={}, languageExtensions=manifest["languageExtensions"],
                        includeSAM=manifest.get("includeSAM", False))
        environment = dict(os.environ, GOMEMLIMIT=args.memory)
        observations = []
        write_report(out, metadata, observations)
        for count in sizes:
            selected = order[:count]
            metadata["selections"][str(count)] = selected
            module = schemas
            if count != len(types):
                generator = executable(args.generator)
                metadata["generatorExecutableSHA256"] = sha(Path(generator))
                source = args.source or manifest["source"]
                module = out / "modules" / str(count)
                command = [generator, "-source", source, "-out", str(module), "-regions", args.region,
                           "-resources", ",".join(selected), "-module", manifest["module"]]
                if manifest["languageExtensions"]:
                    command.append("-language-extensions")
                if manifest.get("includeSAM", any(t.startswith("AWS::Serverless::") for t in types)):
                    command.append("-include-sam")
                subprocess.run(command, cwd=REPO, check=True, stdout=subprocess.DEVNULL)
                generated = json.loads((module / "manifest.json").read_text())
                if generated["sourceSHA256"] != manifest["sourceSHA256"] or region_types(generated, args.region) != {t: types[t] for t in selected}:
                    raise ValueError("filtered source or resource bindings differ from the checked-in module")
            metadata["moduleFingerprints"][str(count)] = schema_fingerprint(module)
            for repeat in range(1, args.repeats + 1):
                # Alternate order between repeats to reduce a fixed cache-order bias.
                for representation in (("full", "types") if repeat % 2 else ("types", "full")):
                    package = "./regions/" + args.region + ("/types" if representation == "types" else "")
                    operations = [("abstract", [cue, "eval", package, "-e", "#Template"])]
                    if template:
                        operations.append(("validation", [cue, "vet", "-c", package, "-d", "#Template", str(template)]))
                    for operation, command in operations:
                        name = f"{count}-{representation}-{operation}-{repeat}"
                        output, errors = out / "logs" / (name + ".stdout"), out / "logs" / (name + ".stderr")
                        result = measure(command, module, args.timeout, environment, output, errors)
                        result.update(resourceCount=count, representation=representation,
                                      operation=operation, repeat=repeat, command=command,
                                      stdout=str(output.relative_to(out)), stderr=str(errors.relative_to(out)))
                        result["status"] = "timeout" if result["timeout"] else "success" if result["exitCode"] == 0 else "rejected" if operation == "validation" else "error"
                        observations.append(result)
                        write_report(out, metadata, observations)
                        print(f"{count} types / {representation} / {operation}: {result['status']} ({result['elapsedSeconds']}s)", flush=True)
        return 1 if any(o["status"] == "error" for o in observations) else 0
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        parser.exit(2, str(error) + "\n")


if __name__ == "__main__":
    raise SystemExit(main())
