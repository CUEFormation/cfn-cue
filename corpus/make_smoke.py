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
    egress = {"GroupId": "sg-12345678", "IpProtocol": "tcp", "FromPort": "443", "ToPort": "443"}
    pair("cidr-host-bits", "AWS::EC2::SecurityGroupEgress", egress, "CidrIp", "10.0.0.0/16", "10.0.1.0/16")
    pair("kms-key-id", "AWS::SNS::Topic", {}, "KmsMasterKeyId", "alias/aws/sns", "aws/sns")
    pair("bucket-name", "AWS::S3::Bucket", {}, "BucketName", "my.bucket-1", "my..bucket")
    cases["dynamic-vpc-id"] = ({"Resources": {"Thing": {"Type": "AWS::EC2::Subnet", "Properties": {
        "CidrBlock": "10.0.0.0/24", "VpcId": "{{resolve:ssm:VpcId:1}}"}}}}, "valid", "valid")
    # Provider patterns such as AmazonMQ's ^[^,:=]+$ apply after resolution.
    broker = {"Resources": {"Broker": {"Type": "AWS::AmazonMQ::Broker", "Properties": {
        "Users": [{"Username": "test", "Password": "{{resolve:secretsmanager:MySecret:SecretString:password}}"}],
        "DeploymentMode": "SINGLE_INSTANCE", "EngineType": "RABBITMQ", "EngineVersion": "3.13",
        "HostInstanceType": "{{resolve:ssm:/broker/instance-type}}", "AutoMinorVersionUpgrade": True,
        "BrokerName": "test", "PubliclyAccessible": False}}}}
    cases["dynamic-reference-pattern-valid"] = (broker, "valid", "valid")
    for name, password in (("literal", "pass:word"), ("resolver", "{{resolve:unknown:MySecret}}")):
        changed = copy.deepcopy(broker)
        changed["Resources"]["Broker"]["Properties"]["Users"][0]["Password"] = password
        cases["dynamic-reference-pattern-" + name + "-invalid"] = (changed, "invalid", "invalid")
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
    # Custom properties are open, so Fn::If and NoValue keys must not also
    # match them.
    conditional = copy.deepcopy(custom)
    conditional["Conditions"] = {"Primary": {"Fn::Equals": [{"Ref": "AWS::Region"}, "us-east-1"]},
                                 "Payload": {"Fn::Equals": [{"Ref": "AWS::Region"}, "us-west-2"]}}
    conditional["Resources"]["Thing"]["Properties"] = {"Fn::If": ["Primary", {"ServiceToken": token},
        {"Fn::If": ["Payload", {"ServiceToken": token, "Payload": [1]}, {"Ref": "AWS::NoValue"}]}]}
    cases["custom-resource-if-properties"] = (conditional, "valid", "valid")
    untokened = copy.deepcopy(conditional)
    untokened["Resources"]["Thing"]["Properties"]["Fn::If"][2]["Fn::If"][1] = {"Payload": [1]}
    cases["custom-resource-if-properties-missing-token"] = (untokened, "invalid", "invalid")
    invalid = copy.deepcopy(custom)
    invalid["Resources"]["Thing"]["Type"] = "Custom::bad!"
    # cfn-lint currently accepts names outside the regional schema's grammar.
    cases["custom-resource-invalid-name"] = (invalid, "invalid", "valid")
    for name, transform in (("transform-null", None), ("transform-list-null", ["AWS::LanguageExtensions", None])):
        cases[name] = ({"Transform": transform, "Resources": {"Queue": {"Type": "AWS::SQS::Queue"}}}, "invalid", "invalid")
    return cases


def service_rule_cases():
    """cfn-lint extension rules, including the shapes that broke CUE evaluation.

    The DynamoDB, ECS task definition and RDS engine version cases cover the
    rules compiled to field-scoped comprehensions."""
    cases = {}

    def resource(name, resource_type, properties, cue_status, lint_status=None):
        template = {"Resources": {"Thing": {"Type": resource_type, "Properties": properties}}}
        cases["rule-" + name] = (template, cue_status, lint_status or cue_status)

    resource("s3-public-acl-ownership-invalid", "AWS::S3::Bucket", {"AccessControl": "PublicRead"}, "invalid")
    resource("s3-public-acl-ownership-valid", "AWS::S3::Bucket", {"AccessControl": "PublicRead",
             "OwnershipControls": {"Rules": [{"ObjectOwnership": "ObjectWriter"}]}}, "valid")
    table = {"BillingMode": "PAY_PER_REQUEST", "KeySchema": [{"AttributeName": "id", "KeyType": "HASH"}],
             "AttributeDefinitions": [{"AttributeName": "id", "AttributeType": "S"}]}
    resource("dynamodb-on-demand-valid", "AWS::DynamoDB::Table", table, "valid")
    resource("target-group-lambda-valid", "AWS::ElasticLoadBalancingV2::TargetGroup", {"TargetType": "lambda"}, "valid")
    resource("target-group-lambda-port-invalid", "AWS::ElasticLoadBalancingV2::TargetGroup",
             {"TargetType": "lambda", "Port": 80, "Protocol": "HTTP"}, "invalid")
    resource("ecs-daemon-valid", "AWS::ECS::Service", {"SchedulingStrategy": "DAEMON", "TaskDefinition": "task"}, "valid")
    resource("ecs-fargate-daemon-invalid", "AWS::ECS::Service",
             {"LaunchType": "FARGATE", "SchedulingStrategy": "DAEMON", "TaskDefinition": "task"}, "invalid")
    resource("stage-method-path-valid", "AWS::ApiGateway::Stage",
             {"RestApiId": "abc", "MethodSettings": [{"HttpMethod": "*", "ResourcePath": "x"}]}, "valid")
    resource("stage-method-path-invalid", "AWS::ApiGateway::Stage",
             {"RestApiId": "abc", "MethodSettings": [{"HttpMethod": "*", "ResourcePath": "x", "MetricsEnabled": True}]}, "invalid")
    instance = {"Engine": "mysql", "DBInstanceClass": "db.t3.micro", "AllocatedStorage": "20",
                "MasterUsername": "admin", "MasterUserPassword": "change-me-1234"}
    resource("rds-backup-retention-valid", "AWS::RDS::DBInstance", dict(instance, BackupRetentionPeriod=7), "valid")
    resource("rds-aurora-exclusive-invalid", "AWS::RDS::DBInstance",
             {"Engine": "Aurora-MySQL", "DBInstanceClass": "db.r5.large", "DBClusterIdentifier": "cluster",
              "MasterUserPassword": "change-me-1234"}, "invalid")
    resource("lambda-zipfile-runtime-invalid", "AWS::Lambda::Function",
             {"Role": "arn:aws:iam::123456789012:role/r", "Runtime": "java21", "Handler": "x", "Code": {"ZipFile": "x"}}, "invalid")
    resource("ecs-task-essential-valid", "AWS::ECS::TaskDefinition",
             {"ContainerDefinitions": [{"Name": "web", "Image": "nginx"}]}, "valid")
    resource("ecs-task-essential-invalid", "AWS::ECS::TaskDefinition",
             {"ContainerDefinitions": [{"Name": "web", "Image": "nginx", "Essential": "false"}]}, "invalid")
    fargate = {"RequiresCompatibilities": ["FARGATE"], "NetworkMode": "awsvpc",
               "ContainerDefinitions": [{"Name": "web", "Image": "nginx"}]}
    resource("ecs-fargate-string-sizes-valid", "AWS::ECS::TaskDefinition", dict(fargate, Cpu="512", Memory="3072"), "valid")
    resource("ecs-fargate-cpu-memory-invalid", "AWS::ECS::TaskDefinition", dict(fargate, Cpu="256", Memory="4096"), "invalid")
    resource("ecs-fargate-network-mode-invalid", "AWS::ECS::TaskDefinition",
             dict(fargate, NetworkMode="bridge", Cpu=256, Memory=512), "invalid")
    keys = {k: table[k] for k in ("KeySchema", "AttributeDefinitions")}
    resource("dynamodb-on-demand-capacity-invalid", "AWS::DynamoDB::Table",
             dict(table, ProvisionedThroughput={"ReadCapacityUnits": 5, "WriteCapacityUnits": 5}), "invalid")
    resource("dynamodb-provisioned-capacity-invalid", "AWS::DynamoDB::Table",
             dict(keys, ProvisionedThroughput={"ReadCapacityUnits": "0", "WriteCapacityUnits": "5"}), "invalid")
    resource("rds-engine-version-invalid", "AWS::RDS::DBInstance", dict(instance, EngineVersion="5.0.1"), "invalid")
    # YAML reads 8.0 as a float; cfn-lint compares str(8.0) with "8.0".
    resource("rds-cluster-float-version-valid", "AWS::RDS::DBCluster",
             {"Engine": "aurora-mysql", "EngineVersion": 8.0, "MasterUsername": "admin",
              "MasterUserPassword": "change-me-1234"}, "valid")
    alarm = {"ComparisonOperator": "GreaterThanThreshold", "EvaluationPeriods": 1, "MetricName": "Errors",
             "Namespace": "AWS/Lambda", "Statistic": "Sum", "Threshold": 1}
    resource("alarm-period-string-valid", "AWS::CloudWatch::Alarm", dict(alarm, Period="300"), "valid")
    resource("alarm-period-invalid", "AWS::CloudWatch::Alarm", dict(alarm, Period=45), "invalid")
    # cfn-lint resolves a Ref to the parameter's values; CUE never lets an
    # intrinsic satisfy a rule's condition.
    ref = {"Parameters": {"Acl": {"Type": "String"}},
           "Resources": {"Thing": {"Type": "AWS::S3::Bucket", "Properties": {"AccessControl": {"Ref": "Acl"}}}}}
    cases["rule-s3-acl-ref-valid"] = (ref, "valid", "valid")
    # Nor under not: a Ref BillingMode does not require ProvisionedThroughput.
    mode = {"Parameters": {"Mode": {"Type": "String", "AllowedValues": ["PAY_PER_REQUEST", "PROVISIONED"]}},
            "Resources": {"Thing": {"Type": "AWS::DynamoDB::Table", "Properties": dict(keys, BillingMode={"Ref": "Mode"})}}}
    cases["rule-dynamodb-billing-mode-ref-valid"] = (mode, "valid", "valid")
    return cases


def template_grammar_cases():
    """Template forms that the full survey found CloudFormation accepts."""
    cases = {}
    # widdix names alarms such as 5xxErrorRateTooHighAlarm.
    cases["logical-id-leading-digit"] = ({"Resources": {"5xxQueue": {"Type": "AWS::SQS::Queue"}}}, "valid", "valid")
    # The VPC Quick Start names conditions such as NATGateway&3AZCondition.
    name = "NATGateway&3AZCondition"
    cases["condition-name-ampersand"] = ({"Parameters": {"Zones": {"Type": "String"}},
        "Conditions": {name: {"Fn::Equals": [{"Ref": "Zones"}, "3"]}},
        "Resources": {"Queue": {"Type": "AWS::SQS::Queue", "Condition": name}}}, "valid", "valid")
    conditional = {"Parameters": {"Name": {"Type": "String"}},
        "Conditions": {"Named": {"Fn::Not": [{"Fn::Equals": [{"Ref": "Name"}, ""]}]}},
        "Resources": {"Bucket": {"Type": "AWS::S3::Bucket", "Properties": {"Fn::If": ["Named",
            {"BucketName": {"Ref": "Name"}, "AccessControl": "Private"}, {"Ref": "AWS::NoValue"}]}}}}
    cases["if-properties"] = (conditional, "valid", "valid")
    wrong = copy.deepcopy(conditional)
    wrong["Resources"]["Bucket"]["Properties"]["Fn::If"][1]["AccessControl"] = "DefinitelyNotAnACL"
    cases["if-properties-branch-invalid"] = (wrong, "invalid", "invalid")
    group = copy.deepcopy(conditional)
    group["Resources"] = {"Group": {"Type": "AWS::AutoScaling::AutoScalingGroup",
        "Properties": {"MinSize": "1", "MaxSize": "2", "AvailabilityZones": ["us-east-1a"],
                       "LaunchTemplate": {"LaunchTemplateId": "lt-0123456789abcdef0", "Version": "1"}},
        "UpdatePolicy": {"Fn::If": ["Named", {"AutoScalingRollingUpdate": {"MinInstancesInService": 1}},
                                    {"Ref": "AWS::NoValue"}]}}}
    cases["if-update-policy"] = (group, "valid", "valid")
    bare = copy.deepcopy(group)
    bare["Resources"]["Group"]["UpdatePolicy"] = {"Ref": "Name"}
    cases["update-policy-ref-invalid"] = (bare, "invalid", "invalid")
    # Every SQS property is optional, so {} matches the properties schema too.
    empty = copy.deepcopy(conditional)
    empty["Resources"] = {"Queue": {"Type": "AWS::SQS::Queue", "Properties": {"Fn::If": ["Named", {}, {"Ref": "AWS::NoValue"}]}}}
    cases["if-properties-empty-branch"] = (empty, "valid", "valid")
    # Fn::If may nest inside a whole Properties branch; literal leaves keep the
    # S3 schema.
    nested = copy.deepcopy(conditional)
    nested["Conditions"]["Versioned"] = {"Fn::Equals": [{"Ref": "Name"}, "versioned"]}
    nested["Conditions"]["Private"] = {"Fn::Equals": [{"Ref": "Name"}, "private"]}
    nested["Resources"]["Bucket"]["Properties"] = {"Fn::If": ["Named",
        {"Fn::If": ["Versioned", {"BucketName": "one-bucket"}, {"BucketName": "two-bucket"}]}, {}]}
    cases["if-properties-nested"] = (nested, "valid", "valid")
    deep = copy.deepcopy(nested)
    deep["Resources"]["Bucket"]["Properties"] = {"Fn::If": ["Named",
        {"Fn::If": ["Versioned", {"Fn::If": ["Private", {"AccessControl": "Private"}, {"Ref": "AWS::NoValue"}]},
                    {}]}, {"Ref": "AWS::NoValue"}]}
    cases["if-properties-nested-deep"] = (deep, "valid", "valid")
    leaf = copy.deepcopy(deep)
    leaf["Resources"]["Bucket"]["Properties"]["Fn::If"][1]["Fn::If"][1]["Fn::If"][1] = {"AccessControl": "DefinitelyNotAnACL"}
    cases["if-properties-nested-leaf-invalid"] = (leaf, "invalid", "invalid")
    arity = copy.deepcopy(nested)
    arity["Resources"]["Bucket"]["Properties"]["Fn::If"][1]["Fn::If"].pop()
    cases["if-properties-nested-arity-invalid"] = (arity, "invalid", "invalid")
    # widdix's wordpress-ha maps engines to PerformanceInsights: true.
    cases["mapping-boolean"] = ({"Mappings": {"Engine": {"mysql": {"PerformanceInsights": True}}},
        "Resources": {"Queue": {"Type": "AWS::SQS::Queue"}}}, "valid", "valid")
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
    # Keep only the sources whose examples are checked in, with their licenses.
    used = {record["source"] for record in records}
    index["sources"] = [entry for entry in index["sources"] if entry["id"] in used]
    for license in (source / "licenses").rglob("*"):
        if license.is_file() and license.relative_to(source / "licenses").parts[0] in used:
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
    cases.update(template_grammar_cases())
    cases.update(service_rule_cases())
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
