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
    "SQS/SQSFIFOQueue.yaml": "valid",
    "EMR/EMRCLusterGangliaWithSparkOrS3backedHbase.yaml": "valid",
    "Solutions/AmazonCloudWatchAgent/inline/redhat.yaml": "valid",
}


def compatibility_cases():
    """Small real-resource positives and negatives for the first fixes."""
    cases = {}

    def pair(name, resource_type, properties, key, good, bad):
        for label, value, status in (("valid", good, "valid"), ("invalid", bad, "invalid")):
            props = copy.deepcopy(properties)
            props[key] = value
            template = {"Resources": {"Thing": {"Type": resource_type, "Properties": props}}}
            cases[name + "-" + label] = (template, status, status)

    pair("quoted-boolean", "AWS::SQS::Queue", {"QueueName": "compatibility.fifo"},
         "FifoQueue", "true", "maybe")
    ingress = {"GroupId": "sg-12345678", "IpProtocol": "tcp", "FromPort": "22", "ToPort": "22", "CidrIp": "10.0.0.0/24"}
    pair("quoted-port", "AWS::EC2::SecurityGroupIngress", ingress, "FromPort", "22", "ssh")
    pair("quoted-retention", "AWS::SQS::Queue", {}, "MessageRetentionPeriod", "60", "1209601")
    pair("vpc-id", "AWS::EC2::Subnet", {"CidrBlock": "10.0.0.0/24"}, "VpcId", "vpc-12345678", "vpc-1234567")
    pair("security-group-id", "AWS::EC2::SecurityGroupIngress", ingress, "GroupId", "sg-12345678", "sg-test")
    pair("ami-id", "AWS::EC2::Instance", {"InstanceType": "t3.micro"}, "ImageId", "ami-12345678", "ami-123456")
    pair("role-arn", "AWS::Events::Rule", {"ScheduleExpression": "rate(5 minutes)", "State": "DISABLED"},
         "RoleArn", "arn:aws:iam::123456789012:role/MyRole", "SomeOptionalParameter")
    cases["dynamic-vpc-id"] = ({"Resources": {"Thing": {"Type": "AWS::EC2::Subnet", "Properties": {
        "CidrBlock": "10.0.0.0/24", "VpcId": "{{resolve:ssm:VpcId:1}}"}}}}, "valid", "valid")
    parameters = {"Parameters": {
        "Text": {"Type": "String", "MinLength": "1", "MaxLength": "18", "NoEcho": "TRUE", "Default": "abc"},
        "Count": {"Type": "Number", "MinValue": "1.5", "MaxValue": "20", "Default": "2"}},
        "Resources": {"Queue": {"Type": "AWS::SQS::Queue"}}}
    cases["quoted-parameter-limits-valid"] = (parameters, "valid", "valid")
    malformed_length = copy.deepcopy(parameters)
    malformed_length["Parameters"]["Text"]["MinLength"] = "not-a-length"
    cases["quoted-parameter-length-invalid"] = (malformed_length, "invalid", "invalid")
    malformed_value = copy.deepcopy(parameters)
    malformed_value["Parameters"]["Count"]["MinValue"] = "not-a-number"
    cases["quoted-parameter-value-invalid"] = (malformed_value, "invalid", "invalid")
    token = "arn:aws:lambda:us-east-1:123456789012:function:CustomResourceProvider"
    custom = {"Resources": {"Thing": {"Type": "Custom::Thing", "Properties": {"ServiceToken": token}}}}
    cases["custom-resource-valid"] = (custom, "valid", "valid")
    intrinsic = copy.deepcopy(custom)
    intrinsic["Resources"]["Thing"]["Properties"]["ServiceToken"] = {"Fn::Sub":
        "arn:${AWS::Partition}:lambda:${AWS::Region}:${AWS::AccountId}:function:CustomResourceProvider"}
    cases["custom-resource-intrinsic"] = (intrinsic, "valid", "valid")
    mixed = copy.deepcopy(custom)
    mixed["Resources"]["Other"] = {"Type": "Custom::Other", "Properties": {"ServiceToken": token, "Payload": {"Enabled": True}}}
    mixed["Resources"]["Canonical"] = {"Type": "AWS::CloudFormation::CustomResource", "Properties": {"ServiceToken": token}}
    cases["custom-resource-mixed"] = (mixed, "valid", "valid")
    missing = copy.deepcopy(custom)
    missing["Resources"]["Thing"]["Properties"] = {}
    cases["custom-resource-missing-token"] = (missing, "invalid", "invalid")
    invalid = copy.deepcopy(custom)
    invalid["Resources"]["Thing"]["Type"] = "Custom::bad!"
    # cfn-lint currently accepts names outside the regional schema's grammar.
    cases["custom-resource-invalid-name"] = (invalid, "invalid", "valid")
    for name, transform in (("transform-null", None), ("transform-list-null", ["AWS::LanguageExtensions", None])):
        cases[name] = ({"Transform": transform, "Resources": {"Queue": {"Type": "AWS::SQS::Queue"}}}, "invalid", "invalid")
    return cases


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
                note = "Regression: quoted CloudFormation boolean is accepted without changing the original input."
            elif record["path"].startswith("EMR/"):
                note = "Regression: object-valued Fn::If stays complete through a locally referenced schema."
            elif record["path"].endswith("inline/redhat.yaml"):
                note = "Regression: quoted parameter length limits and resource ports remain accepted without rewriting the input."
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
    cases.update(compatibility_cases())
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
                     "Intentional mutation: " + name + (". Known lint gap: the read-only property is accepted by cfn-lint." if name == "read-only-property" else
                     ". Known lint gap: the malformed custom type name is accepted by cfn-lint." if name == "custom-resource-invalid-name" else ""))
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
