"""Rebuild the project-authored semantic reference corpus.

Every case is checked with the validator's --checks all mode and with cfn-lint.
Unlike the smoke corpus, these cases need no downloaded examples.
"""
import argparse
import copy
import json
from pathlib import Path

import yaml

import corpus

NO_VALUE = {"Ref": "AWS::NoValue"}


def base():
    """A template using each reference form CloudFormation resolves."""
    return {
        "Parameters": {"Env": {"Type": "String", "AllowedValues": ["prod", "dev"]},
                       "Prefix": {"Type": "String"}},
        "Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]},
                       "Dev": {"Fn::Equals": [{"Ref": "Env"}, "dev"]}},
        "Mappings": {"Sizes": {"prod": {"Delay": "5"}, "dev": {"Delay": "0"}}},
        "Resources": {
            "Bucket": {"Type": "AWS::S3::Bucket", "Properties": {
                "BucketName": {"Fn::Sub": "${Prefix}-${AWS::Region}-${AWS::AccountId}"}}},
            "Queue": {"Type": "AWS::SQS::Queue", "DependsOn": "Bucket", "Properties": {
                "QueueName": {"Fn::Sub": ["${Name}-queue", {"Name": {"Ref": "Prefix"}}]},
                "DelaySeconds": {"Fn::FindInMap": ["Sizes", {"Ref": "Env"}, "Delay"]},
                "Tags": [
                    {"Key": "bucket", "Value": {"Fn::GetAtt": ["Bucket", "Arn"]}},
                    {"Key": "domain", "Value": {"Fn::Sub": "${Bucket.DomainName}/${!Literal}"}},
                    {"Fn::If": ["Prod", {"Key": "env", "Value": {"Ref": "Bucket"}}, NO_VALUE]}]}},
            "Topic": {"Type": "AWS::SNS::Topic", "Condition": "Dev", "Properties": {
                "TopicName": {"Fn::Sub": "${Prefix}-topic"}}},
        },
        "Outputs": {
            "QueueUrl": {"Value": {"Ref": "Queue"}},
            "QueueName": {"Value": {"Fn::GetAtt": "Queue.QueueName"}},
            "QueueArn": {"Value": {"Fn::GetAtt": ["Queue", "Arn"]},
                         "Export": {"Name": {"Fn::Sub": "${AWS::StackName}-queue"}}},
            "Topic": {"Condition": "Dev", "Value": {"Ref": "Topic"}},
        },
    }


def queues(**resources):
    """Two or more SQS queues, which have no required properties."""
    template = {"Parameters": {"Env": {"Type": "String", "AllowedValues": ["prod", "dev"]}},
                "Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]},
                               "Dev": {"Fn::Equals": [{"Ref": "Env"}, "dev"]}},
                "Resources": {}}
    for name, properties in resources.items():
        resource = {"Type": "AWS::SQS::Queue"}
        resource.update(properties)
        template["Resources"][name] = resource
    return template


def role(statements):
    """An IAM role whose inline policy has the given statements."""
    return {"Parameters": {"Env": {"Type": "String", "AllowedValues": ["prod", "dev"]}},
            "Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]}},
            "Resources": {"Role": {"Type": "AWS::IAM::Role", "Properties": {
                "AssumeRolePolicyDocument": {"Version": "2012-10-17", "Statement": [{
                    "Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]},
                "Policies": [{"PolicyName": "read", "PolicyDocument": {"Version": "2012-10-17", "Statement": statements}}]}}}}


def statement(sid, bucket):
    return {"Sid": sid, "Effect": "Allow", "Action": "s3:GetObject", "Resource": "arn:aws:s3:::" + bucket + "/*"}


def logs(**resources):
    """Log groups, whose primary identifier LogGroupName a template may set."""
    template = queues()
    for name, resource in resources.items():
        template["Resources"][name] = dict({"Type": "AWS::Logs::LogGroup"}, **copy.deepcopy(resource))
    return template


def cases():
    """Each case maps to (template, cue status, lint status, note)."""
    out = {}

    def case(name, template, cue, lint=None, note=""):
        out[name] = (template, cue, lint or cue, note)

    def mutate(name, change, cue="invalid", lint=None, note=""):
        template = base()
        change(template)
        case(name, template, cue, lint, note)

    case("references-valid", base(), "valid")
    stack = {"Resources": {
        "Child": {"Type": "AWS::CloudFormation::Stack", "Properties": {"TemplateURL": "https://example.com/child.yaml"}},
        "Queue": {"Type": "AWS::SQS::Queue", "Properties": {
            "QueueName": {"Fn::GetAtt": ["Child", "Outputs.QueueName"]},
            "KmsMasterKeyId": {"Fn::Sub": "${Child.Outputs.KeyId}"}}}}}
    case("getatt-stack-output", stack, "valid", note="Nested stack outputs are any Outputs.<name>.")
    custom = {"Resources": {
        "Provider": {"Type": "Custom::Provider", "Properties": {
            "ServiceToken": {"Fn::Sub": "arn:${AWS::Partition}:lambda:${AWS::Region}:${AWS::AccountId}:function:provider"}}},
        "Queue": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::GetAtt": ["Provider", "Any.Attribute"]}}}}}
    case("getatt-custom-resource", custom, "valid", note="Custom resources return provider-defined attributes.")
    instance = {"Resources": {
        "Instance": {"Type": "AWS::EC2::Instance", "Properties": {"ImageId": "ami-12345678", "InstanceType": "t3.micro"}},
        "Volume": {"Type": "AWS::EC2::Volume", "Properties": {
            "Size": 10, "AvailabilityZone": {"Fn::GetAtt": ["Instance", "AvailabilityZone"]}}}}}
    case("getatt-writable-property", instance, "valid",
         note="cfn-lint's attribute list lets EC2 instances return every property path.")
    computed = base()
    computed["Parameters"]["Attribute"] = {"Type": "String", "AllowedValues": ["Arn", "QueueName"]}
    computed["Outputs"]["Computed"] = {"Value": {"Fn::GetAtt": ["Queue", {"Ref": "Attribute"}]}}
    case("getatt-unresolved-attribute", computed, "valid",
         note="An attribute name computed by Ref stays unresolved; it is not checked.")
    # The common pattern for a conditional dependency: a Fn::If in Metadata.
    conditional = queues(A={"Metadata": {"Wait": {"Fn::If": ["Prod", {"Ref": "B"}, ""]}}}, B={})
    case("conditional-dependency", conditional, "valid")

    mutate("ref-undefined", lambda t: t["Resources"]["Queue"]["Properties"].update(
        QueueName={"Ref": "Missing"}))
    mutate("ref-resource-in-condition", lambda t: t["Conditions"].update(
        HasBucket={"Fn::Equals": [{"Ref": "Bucket"}, "x"]}))
    mutate("ref-duplicate-logical-id", lambda t: t["Parameters"].update(Queue={"Type": "String"}))
    mutate("getatt-undefined-resource", lambda t: t["Outputs"]["QueueArn"].update(
        Value={"Fn::GetAtt": ["Missing", "Arn"]}))
    mutate("getatt-parameter", lambda t: t["Outputs"]["QueueArn"].update(
        Value={"Fn::GetAtt": ["Prefix", "Arn"]}))
    mutate("getatt-attribute-invalid", lambda t: t["Outputs"]["QueueArn"].update(
        Value={"Fn::GetAtt": ["Queue", "Url"]}))
    mutate("getatt-string-attribute-invalid", lambda t: t["Outputs"]["QueueName"].update(
        Value={"Fn::GetAtt": "Queue.Name"}))
    mutate("getatt-stack-attribute-invalid", lambda t: t["Resources"].update(
        Child={"Type": "AWS::CloudFormation::Stack", "Properties": {"TemplateURL": {"Fn::GetAtt": ["Queue", "Arn"]}}},
        Other={"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::GetAtt": ["Child", "QueueName"]}}}))
    mutate("sub-undefined-variable", lambda t: t["Resources"]["Bucket"]["Properties"].update(
        BucketName={"Fn::Sub": "${Prefix}-${Missing}"}))
    mutate("sub-unbound-list-variable", lambda t: t["Resources"]["Queue"]["Properties"].update(
        QueueName={"Fn::Sub": ["${Name}-${Other}", {"Name": "x"}]}))
    mutate("sub-attribute-invalid", lambda t: t["Outputs"]["QueueUrl"].update(
        Value={"Fn::Sub": "${Queue.Url}"}))
    mutate("dependson-undefined", lambda t: t["Resources"]["Queue"].update(DependsOn=["Bucket", "Missing"]))
    mutate("condition-undefined", lambda t: t["Resources"]["Topic"].update(Condition="Staging"))
    mutate("if-condition-undefined", lambda t: t["Resources"]["Queue"]["Properties"]["Tags"][2]["Fn::If"].__setitem__(
        0, "Staging"))
    mutate("condition-cycle", lambda t: t["Conditions"].update(
        Prod={"Fn::And": [{"Fn::Equals": [{"Ref": "Env"}, "prod"]}, {"Condition": "Dev"}]},
        Dev={"Fn::Not": [{"Condition": "Prod"}]}), "invalid", "valid",
        note="Known divergence: Prod and Dev refer to each other. cfn-lint has no circular-condition check.")
    mutate("mapping-undefined", lambda t: t["Resources"]["Queue"]["Properties"].update(
        DelaySeconds={"Fn::FindInMap": ["Missing", {"Ref": "Env"}, "Delay"]}))
    mutate("mapping-key-invalid", lambda t: t["Resources"]["Queue"]["Properties"].update(
        DelaySeconds={"Fn::FindInMap": ["Sizes", "prod", "Delays"]}))

    case("cycle-self", queues(A={"DependsOn": "A"}), "invalid")
    case("cycle-dependson", queues(A={"DependsOn": "B"}, B={"DependsOn": "A"}), "invalid")
    case("cycle-references", queues(
        A={"Properties": {"QueueName": {"Fn::GetAtt": ["B", "QueueName"]}}},
        B={"Properties": {"QueueName": {"Fn::Sub": "${C}-b"}}},
        C={"Properties": {"QueueName": {"Ref": "A"}}}), "invalid")
    case("cycle-both-branches", queues(
        A={"Properties": {"QueueName": {"Fn::If": ["Prod", {"Ref": "B"}, {"Fn::Sub": "${B}-dev"}]}}},
        B={"DependsOn": "A"}), "invalid", note="Both branches refer to B, so the cycle exists for every Env.")
    case("cycle-conditional", queues(
        A={"Properties": {"QueueName": {"Fn::If": ["Prod", {"Ref": "B"}, "a"]}}},
        B={"DependsOn": "A"}), "valid", "invalid",
        note="Known divergence: the cycle exists only when Prod holds, which depends on a parameter. "
             "The validator reports a warning; cfn-lint ignores conditions and reports E3004.")
    case("cycle-exclusive-conditions", queues(
        A={"Condition": "Prod", "DependsOn": "B"},
        B={"Condition": "Dev", "DependsOn": "A"}), "valid", "invalid",
        note="Known divergence: Prod and Dev compare Env with different values, so the resources never exist "
             "together and no cycle forms. Each DependsOn names a resource absent whenever its dependent exists; "
             "the validator warns because that depends on Env. cfn-lint reports E3004 and E3005.")

    # References to a resource with a Condition.
    mutate("conditional-target-guarded", lambda t: t["Resources"]["Queue"]["Properties"]["Tags"].append(
        {"Fn::If": ["Dev", {"Key": "topic", "Value": {"Ref": "Topic"}}, NO_VALUE]}), "valid")
    mutate("conditional-target-excluded", lambda t: t["Resources"]["Queue"]["Properties"]["Tags"].append(
        {"Fn::If": ["Dev", NO_VALUE, {"Key": "topic", "Value": {"Ref": "Topic"}}]}), "valid",
        note="The branch is taken only when Topic is absent. Deployment fails for those Env values, so the "
             "validator warns; cfn-lint warns with W1001.")
    never = base()
    never["Conditions"]["Never"] = {"Fn::Equals": ["a", "b"]}
    never["Resources"]["Topic"]["Condition"] = "Never"
    never["Outputs"]["Topic"]["Condition"] = "Never"
    never["Resources"]["Bucket"]["Properties"]["Tags"] = [{"Key": "topic", "Value": {"Ref": "Topic"}}]
    case("conditional-target-never", never, "invalid", "valid",
         note="Known divergence: the Never condition compares two different literals, so Topic never exists "
              "and the unconditional Ref fails for every parameter value. cfn-lint warns with W1001.")
    mutate("conditional-target-ref", lambda t: t["Resources"]["Queue"]["Properties"]["Tags"].append(
        {"Key": "topic", "Value": {"Ref": "Topic"}}), "valid",
        note="Topic exists only when Dev holds. The validator and cfn-lint (W1001) both warn without rejecting.")
    mutate("conditional-target-dependson", lambda t: t["Resources"]["Bucket"].update(DependsOn="Topic"), "valid", "invalid",
        note="Known divergence: Topic is absent when Dev is false, which depends on a parameter. The validator "
             "warns; cfn-lint reports E3005.")

    case("sid-distinct", role([statement("ReadA", "a"), statement("ReadB", "b")]), "valid")
    case("sid-duplicate", role([statement("Read", "a"), statement("Read", "b")]), "invalid")
    case("sid-exclusive-branches", role([{"Fn::If": ["Prod", statement("Read", "a"), NO_VALUE]},
                                         {"Fn::If": ["Prod", NO_VALUE, statement("Read", "b")]}]), "valid",
         note="The two statements are on opposite branches of Prod, so they never appear together.")
    case("sid-conditional", role([statement("Read", "a"), {"Fn::If": ["Prod", statement("Read", "b"), NO_VALUE]}]),
         "valid",
         note="The duplicate exists only when Prod holds, which depends on a parameter. The validator warns; "
              "cfn-lint reports nothing.")
    case("sid-statement-branch", role({"Fn::If": ["Prod", [statement("Read", "a"), statement("Read", "b")],
                                                  [statement("Read", "a")]]}), "valid", "invalid",
         note="Known divergence: only the Prod list repeats the Sid, which depends on a parameter. The validator "
              "warns; cfn-lint checks each Statement branch and reports E3510.")
    bucket_policy = {"Resources": {
        "Bucket": {"Type": "AWS::S3::Bucket"},
        "Policy": {"Type": "AWS::S3::BucketPolicy", "Properties": {"Bucket": {"Ref": "Bucket"}, "PolicyDocument": {
            "Version": "2012-10-17", "Statement": [
                dict(statement("Read", "a"), Principal="*", Resource={"Fn::Sub": "${Bucket.Arn}/a/*"}),
                dict(statement("Read", "b"), Principal="*", Resource={"Fn::Sub": "${Bucket.Arn}/b/*"})]}}}}}
    case("sid-bucket-policy", bucket_policy, "valid", note="S3 bucket policies may repeat a Sid.")
    # Two resources of one type with the same primary identifier (cfn-lint E3019).
    named = {"Properties": {"LogGroupName": "app"}}
    case("identifier-duplicate", logs(A=named, B=named), "invalid")
    case("identifier-distinct", logs(A=named, B={"Properties": {"LogGroupName": "other"}}), "valid")
    case("identifier-generated", logs(A=named, B={}), "valid",
         note="CloudFormation generates a missing LogGroupName, so it cannot collide.")
    case("identifier-read-only", queues(A={"Properties": {"QueueName": "app"}}, B={"Properties": {"QueueName": "app"}}),
         "valid", note="The SQS queue identifier QueueUrl is read-only, so neither validator compares QueueName.")
    stage = {"Type": "AWS::ApiGateway::Stage", "Properties": {"RestApiId": "abc123", "StageName": "prod"}}
    composite = logs()
    composite["Resources"].update(A=stage, B=copy.deepcopy(stage), C=copy.deepcopy(stage))
    composite["Resources"]["C"]["Properties"]["StageName"] = "dev"
    case("identifier-composite", composite, "invalid", note="A and B share both parts of the RestApiId/StageName identifier.")
    case("identifier-exclusive-conditions", logs(A=dict(named, Condition="Prod"), B=dict(named, Condition="Dev")), "valid",
         note="Prod and Dev compare Env with different values, so A and B never exist together.")
    case("identifier-if-branches", logs(
        A={"Properties": {"LogGroupName": {"Fn::If": ["Prod", "app", "app-dev"]}}},
        B={"Properties": {"LogGroupName": {"Fn::If": ["Prod", "app", "app-dev"]}}}), "invalid",
        note="Each Fn::If branch collides, so the duplicate exists for every Env.")
    case("identifier-if-exclusive", logs(
        A={"Properties": {"LogGroupName": {"Fn::If": ["Prod", "app", NO_VALUE]}}},
        B={"Properties": {"LogGroupName": {"Fn::If": ["Prod", NO_VALUE, "app"]}}}), "valid",
        note="A is named app only when Prod holds and B only when it does not.")
    case("identifier-conditional", logs(A=dict(named, Condition="Prod"), B=named), "valid", "invalid",
         note="Known divergence: A and B collide only when Prod holds, which depends on a parameter. The validator "
              "warns; cfn-lint reports E3019.")
    case("identifier-number-string", logs(
        A={"Properties": {"LogGroupName": 9007199254740993}},
        B={"Properties": {"LogGroupName": "9007199254740993"}}), "invalid",
        note="A number matches the string with the same digits, as cfn-lint compares it.")
    case("identifier-large-integers", logs(
        A={"Properties": {"LogGroupName": 9007199254740992}},
        B={"Properties": {"LogGroupName": 9007199254740993}}), "valid",
        note="Numbers keep every digit, so integers beyond float64 precision stay distinct.")
    case("identifier-number-spelling", logs(
        A={"Properties": {"LogGroupName": 1}},
        B={"Properties": {"LogGroupName": 1.0}}), "valid", "invalid",
        note="Known divergence: the validator compares numbers as written, so 1 and 1.0 differ. cfn-lint compares "
             "two numbers by value and reports E3019.")
    same = {"Properties": {"LogGroupName": {"Fn::Sub": "${AWS::StackName}-app"}}}
    case("identifier-intrinsic", logs(A=same, B=same), "valid", "invalid",
         note="Known divergence: the validator compares only literal identifiers and leaves Fn::Sub unresolved. "
              "cfn-lint compares the expressions and reports E3019.")
    return out


def build(output):
    lock_bytes = (corpus.HERE / "sources.lock.json").read_bytes()
    records, expected = [], {}
    for name, (template, cue_status, lint_status, note) in sorted(cases().items()):
        cid = "semantic-" + name
        raw = yaml.safe_dump(template, sort_keys=False).encode()
        decoded, normalized = corpus.normalize(raw)
        original = "original/semantic/" + name + ".yaml"
        path = "normalized/" + cid + ".json"
        corpus.write(output / original, raw)
        corpus.write(output / path, normalized)
        records.append(dict(id=cid, source="semantic", path=name, original=original, normalized=path,
                            original_sha256=corpus.digest(raw), normalized_sha256=corpus.digest(normalized),
                            url="https://github.com/CUEFormation/cfn-cue", license="Apache-2.0",
                            **corpus.features(decoded)))
        if not note:
            note = "Project-authored valid template." if cue_status == "valid" else "Intentional reference error: " + name + "."
        for region in ("us-east-1", "us-west-2"):
            expected[cid + ":" + region] = dict(cue=cue_status, lint=lint_status, note=note)
    index = dict(version=1, source_lock_sha256=corpus.digest(lock_bytes), sources=[],
                 normalizer=corpus.versions(), templates=records, skipped=[])
    corpus.write(output / "index.json", corpus.canonical(index))
    corpus.write(output / "expectations.json", corpus.canonical(expected))
    print(f"Wrote {len(records)} templates, {len(expected)} region contracts to {output}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=corpus.HERE / "semantic")
    build(parser.parse_args().out)
