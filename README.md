# cfn-cue

[CUE](https://cuelang.org) schemas for every AWS CloudFormation resource type,
generated from the schemas AWS publishes for each region.

A CloudFormation template can contain a misspelled property, a value outside
an enum, or a missing required field, and you usually find out when the
deploy fails. cfn-cue moves those errors to your editor or CI. You can use it
two ways:

- **Check the templates you already have.** Run `cue vet` against your YAML or
  JSON and get an error that names the field before anything reaches AWS.
- **Write templates in CUE.** Import a typed definition for each resource, put
  repeated values in variables and shared definitions, add your own rules on
  top, and export plain CloudFormation YAML.

The schemas cover the parts of a template that are easy to get wrong:

- Resource properties: required fields, unknown keys, types, enums, patterns
  and numeric limits.
- Intrinsic functions such as `Fn::Sub`, `Fn::If` and `Ref`, nested to any
  depth, checked for shape and for the kind of value they can return. The
  return type of a `Ref` or `Fn::GetAtt` depends on the template, so it isn't
  checked.
- Regional availability, so a resource type fails in a region that doesn't
  offer it.
- IAM policy documents, checked against the rules for each kind of policy
  (trust, identity, bucket, key, SCP).
- A set of cfn-lint's service rules, such as the Fargate CPU and memory pairs.

The generated module is checked in, so you can use it without running the
generator.

## Check an existing template

You need the [CUE CLI](https://cuelang.org/docs/introduction/installation/),
v0.18.0-rc.1 or later. Take this template:

```yaml
Resources:
  Bucket:
    Type: AWS::S3::Bucket
    Properties:
      VersioningConfiguration:
        Status: On
  Queue:
    Type: AWS::SQS::Queue
    Properties:
      VisibilityTimeout: 50000
```

Check it against the `us-east-1` schemas from the root of the generated module:

```console
$ git clone https://github.com/CUEFormation/cfn-cue.git
$ cd cfn-cue/generated
$ cue vet -c ./regions/us-east-1 -d '#Template' ~/stack.yaml
Resources.Bucket.Properties.VersioningConfiguration.Status: conflicting values "Enabled" and "On"
Resources.Queue.Properties.VisibilityTimeout: invalid value 50000 (out of bound <=43200)
...
```

`cue vet` doesn't read CloudFormation's short tags (`!Ref`, `!Sub`), so write
them out as `Ref:` and `Fn::Sub:`. Loading a whole region takes about two
seconds.

The repository also includes a Go validator. It loads only the schemas a
template uses, so a typical template takes tens of milliseconds. It also checks
references the schemas can't see, such as a `Ref` to a resource that doesn't
exist. It reads JSON. Run it from the repository root:

```sh
cd ..
go run ./cmd/cfn-cue-validate -template stack.json -region us-east-1 -checks all
```

## Write templates in CUE

Each resource type has its own package. Copy `generated/` somewhere outside
the repository, add your files to the copy, and import the types you use. Don't
keep your own files in `generated/` itself: regenerating replaces the whole
directory.

```cue
package stack

import (
	bucket "github.com/cueformation/cfn-cue/AWS/S3/Bucket"
	queue "github.com/cueformation/cfn-cue/AWS/SQS/Queue"
)

_tags: [bucket.#Tag & {Key: "team", Value: "data"}]

Resources: {
	Logs: bucket.#Resource & {
		Type: "AWS::S3::Bucket"
		Properties: {
			BucketName: {"Fn::Sub": "${AWS::StackName}-logs"}
			VersioningConfiguration: Status: "Enabled"
			Tags: _tags
		}
	}
	Jobs: queue.#Resource & {
		Type: "AWS::SQS::Queue"
		Properties: VisibilityTimeout: 300
	}
}
```

`cue export --out yaml` turns this into an ordinary CloudFormation template.
A mistake fails in CUE before it can reach AWS.

Your own rules work the same way. This definition makes every bucket that uses
it block public access:

```cue
#PrivateBucket: bucket.#Resource & {
	Properties: PublicAccessBlockConfiguration: {
		BlockPublicAcls:       true
		BlockPublicPolicy:     true
		IgnorePublicAcls:      true
		RestrictPublicBuckets: true
	}
}

Resources: Logs: #PrivateBucket & {
	Type: "AWS::S3::Bucket"
	Properties: PublicAccessBlockConfiguration: BlockPublicPolicy: false
}
// Resources.Logs.Properties.PublicAccessBlockConfiguration.BlockPublicPolicy:
//     conflicting values true and false
```

To check a whole template against a region, import
`github.com/cueformation/cfn-cue/regions/us-east-1:cf` and unify with
`cf.#Template`. The module isn't published to a CUE registry yet. To use it
from another module, copy `generated/` as above or publish it yourself.

## What it doesn't do

The CUE schemas check each resource on its own. They don't evaluate
conditions or intrinsic functions, run transforms, or know about relationships
between services. The Go validator goes further: it checks references and
names, and works out which `Fn::If` branches each condition can reach, but it
still doesn't resolve values at deploy time. Keep
[cfn-lint](https://github.com/aws-cloudformation/cfn-lint) in your workflow.
The two catch different mistakes. See the
[validation boundary](docs/schemas.md#validation-boundary) for the details.

## Regenerating

The checked-in module was built from the AWS schema snapshot in `snapshots/`.
With Go 1.26 or later, run these from the repository root to rebuild it from
that snapshot, fetch the latest schemas from AWS, or build a smaller module for
a few regions and types:

```sh
go run ./cmd/cfn-cue -source snapshots/schemas-2026-10-12.zip -out generated
go run ./cmd/cfn-cue -out generated
go run ./cmd/cfn-cue -out sample -regions us-east-1 \
  -resources AWS::S3::Bucket,AWS::SQS::Queue
```

[docs/development.md](docs/development.md) covers the options, tests and
benchmarks.

## Documentation

- [Generated schemas](docs/schemas.md): module layout, imports, intrinsic
  functions, and what is and isn't checked
- [Semantic validation](docs/semantic-validation.md): references, names and
  dependency cycles
- [Conditional removal with AWS::NoValue](docs/novalue.md) and
  [condition models](docs/conditions.md)
- [IAM policies](docs/iam-policies.md)
- [Service rules from cfn-lint](docs/cfn-lint-rules.md)
- [Identifier formats](docs/format-policy.md) and
  [regex lookarounds](docs/regex-lookarounds.md)
- [Regional packages and performance](docs/regional-performance.md) and
  [BENCHMARKS.md](BENCHMARKS.md)
- [Example corpus](corpus/README.md): how the schemas are tested against more
  than 2,000 public templates
- [Development](docs/development.md) and [RESEARCH.md](RESEARCH.md)

This is a rewrite of the 2021 generator in
[cue-sh/cfn-cue](https://github.com/cue-sh/cfn-cue).

## License

The generator is licensed under [Apache-2.0](LICENSE). Bundled AWS schema data
and generated definitions derived from it use the upstream
[MIT-0 license](snapshots/LICENSE.aws); see
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
