"""Rebuild the small checked-in corpus from pinned downloads and mutations."""
import argparse
import copy
import json
from pathlib import Path

import yaml

import corpus

SAMPLES = {
    "SNS/SNSTopic.yaml": "valid",
    "EFS/EFSAccessPoint.yaml": "valid",
    "ServiceCatalog/Portfolio.yaml": "valid",
    "EC2/InstanceWithCfnInit.yaml": "valid",
    "test/fixtures/templates/good/conditions/and.yaml": "valid",
    "test/fixtures/templates/good/resources/iam/managed_policy_description.yaml": "valid",
    "test/fixtures/templates/good/resources/lambda/required_properties.yaml": "valid",
    "test/fixtures/templates/good/resources/properties/templated_code.yaml": "valid",
    "SQS/SQSFIFOQueue.yaml": "invalid",
    "EMR/EMRCLusterGangliaWithSparkOrS3backedHbase.yaml": "invalid",
}


def build(source, output):
    index = json.loads((source / "index.json").read_bytes())
    records, expected = [], {}
    for record in index["templates"]:
        if record["path"] not in SAMPLES:
            continue
        records.append(record)
        for key in ("original", "normalized", "long_yaml"):
            relative = record[key]
            corpus.write(output / relative, (source / relative).read_bytes())
        for region in ("us-east-1", "us-west-2"):
            note = "Pinned upstream example."
            if record["path"] == "SQS/SQSFIFOQueue.yaml":
                note = "Known gap: CloudFormation accepts the quoted boolean 'true'; CUE currently requires bool."
            elif record["path"].startswith("EMR/"):
                note = "Known gap: CUE reports an incomplete object-valued Fn::If in Applications; the three IAM resources pass in isolation."
            expected[record["id"] + ":" + region] = dict(cue=SAMPLES[record["path"]], lint="valid", note=note)
    if len(records) != len(SAMPLES):
        raise ValueError("pinned sample missing from source corpus")
    for license in (source / "licenses").rglob("*"):
        if license.is_file():
            corpus.write(output / license.relative_to(source), license.read_bytes())
    base = {"Parameters": {"Name": {"Type": "String"}},
            "Conditions": {"Create": {"Fn::Equals": [{"Ref": "Name"}, "yes"]}},
            "Resources": {"Bucket": {"Type": "AWS::S3::Bucket", "Properties": {
                "BucketName": {"Fn::Join": ["-", [{"Fn::Sub": ["${P}-bucket", {"P": {"Ref": "Name"}}]}, {"Ref": "Name"}]]},
                "AccessControl": {"Fn::If": ["Create", "Private", {"Ref": "AWS::NoValue"}]}}}},
            "Outputs": {"BucketArn": {"Value": {"Fn::GetAtt": ["Bucket", "Arn"]}}}}
    cases = {"nested-intrinsics": (copy.deepcopy(base), "valid", "valid")}
    changes = {
        "unknown-property": ("Unexpected", True),
        "enum": ("AccessControl", "DefinitelyNotAnACL"),
        "wrong-function-result": ("BucketName", {"Fn::Split": [",", "x"]}),
        "wrong-function-arity": ("BucketName", {"Fn::Sub": ["x"]}),
        "two-function-keys": ("BucketName", {"Fn::Sub": "x", "Ref": "Name"}),
        "wrong-if-branch": ("BucketName", {"Fn::If": ["Create", [], "x"]}),
        "read-only-property": ("Arn", "read-only"),
    }
    for name, (key, value) in changes.items():
        case = copy.deepcopy(base)
        case["Resources"]["Bucket"]["Properties"][key] = value
        cases[name] = (case, "invalid", "valid" if name == "read-only-property" else "invalid")
    cases["missing-required"] = ({"Resources": {"Role": {"Type": "AWS::IAM::Role", "Properties": {"RoleName": "missing-policy"}}}}, "invalid", "invalid")
    cases["unknown-resource"] = ({"Resources": {"Thing": {"Type": "AWS::DoesNotExist::Thing"}}}, "unsupported", "invalid")
    for name, (value, cue_status, lint_status) in sorted(cases.items()):
        cid = "mutation-" + name
        raw = yaml.safe_dump(value, sort_keys=False).encode()
        decoded, normalized = corpus.normalize(raw)
        original = "original/mutations/" + name + ".yaml"
        path = "normalized/" + cid + ".json"
        corpus.write(output / original, raw)
        corpus.write(output / path, normalized)
        records.append(dict(id=cid, source="mutations", path=name, original=original,
                            normalized=path, original_sha256=corpus.digest(raw), normalized_sha256=corpus.digest(normalized),
                            url="https://github.com/CUEFormation/cfn-cue", license="Apache-2.0", **corpus.features(decoded)))
        for region in ("us-east-1", "us-west-2"):
            expected[cid + ":" + region] = dict(cue=cue_status, lint=lint_status,
                note="Project-authored valid template." if cue_status == "valid" else
                     "Intentional mutation: " + name + (". Known lint gap: the read-only property is accepted by cfn-lint." if name == "read-only-property" else ""))
    index["templates"], index["skipped"] = records, []
    corpus.write(output / "index.json", corpus.canonical(index))
    corpus.write(output / "expectations.json", corpus.canonical(expected))
    print(f"Wrote {len(records)} templates, {len(expected)} region contracts to {output}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus", type=Path, default=Path(".corpus"))
    parser.add_argument("--out", type=Path, default=corpus.HERE / "smoke")
    args = parser.parse_args()
    build(args.corpus, args.out)
