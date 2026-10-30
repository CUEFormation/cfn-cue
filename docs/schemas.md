# Generated schemas

What the generator emits, how to import it, and what the schemas do and do not check. For a shorter introduction, see the [README](../README.md).

## Generated module

```text
generated/
  cue.mod/module.cue
  fn/functions.cue                 Intrinsic function definitions
  template/template.cue            Shared template sections and resource envelope
  iam/iam.cue                      IAM policy grammar and per-placement profiles
  literal/h<hash>/schema.cue        #Properties and definitions for literal resource data
  resources/h<hash>/schema.cue      #Resource with inline properties, and definitions with intrinsics
  regions/<region>/region.cue      #Template, #Resources, #ResourceTypes
  regions/<region>/types/region.cue #Template, #Resources, #Resource, #ResourceType
  regions/<region>/index.json      Module, options, and type-to-hash map for one region
  <Org>/<Service>/<Name>/resource.cue
                                   #Resource and definitions for one type, re-exported
  manifest.json                   Source checksum, region list, per-type schema and availability, schema lifecycle metadata, conversion report
```

The two resource representations serve different purposes. `literal/` preserves resource data shapes, including read-only properties. `resources/` describes inputs to a template: it removes read-only properties and permits expressions at value boundaries. Required fields, closed objects, numeric limits, enums, arrays, local references and schema applicators are handled by the CUE JSON Schema extractor. Presence rules between properties are emitted as CUE directly, so errors name the fields. This covers mutually exclusive properties and exactly-one-of or at-least-one-of groups (for example `CidrIpv6 cannot be used with CidrIp; set exactly one of CidrIp, CidrIpv6, ...`). Groups that name several properties per alternative, or names that aren't capitalized identifiers, keep the extractor's `matchN` form.

In `resources/`, `#Resource` holds the properties schema inline, so a file reads from the resource envelope to its properties and then the definitions they use. CUE can't select an optional or required field, so to reuse the properties schema on its own write `(s3.#Resource & {Properties: _}).Properties`. `literal/` packages have no envelope and keep `#Properties`.

Each provider definition becomes a package-level definition, such as `#Tag` or `#TargetResource`, so other packages can reference it. `resources/` packages expose only the definitions reachable from `#Resource`; a definition used only by read-only properties, or by a policy replaced with an IAM profile, is omitted. `literal/` packages keep the full upstream set. Read-only removal applies to property paths, so a shared definition that stays reachable keeps the provider's complete shape. A definition named `Properties` or `Resource`, or one whose name is not an identifier, stays in the extractor's hidden `_#defs` namespace.

Template-input schemas also permit CloudFormation boolean spellings and numeric
strings, checking parsed numbers against the property's numeric constraints.
The original spelling is retained; this is generated CUE validation, not a YAML
rewrite. Literal resource schemas keep strict primitive types. Enum and const
constraints conservatively retain their upstream value types in this batch;
cfn-lint accepts some quoted enum values that these schemas still reject.

Supported boolean strings are `true`, `True`, `TRUE`, `false`, `False`, and
`FALSE`. Numeric strings use finite ASCII decimal syntax, including signs,
whitespace, digit separators, and number exponents; integer strings do not
accept fractions or exponents. Unicode numeral conversions and non-finite
numbers remain unsupported. Composed scalar constraints whose exact-value
semantics cannot be established stay strict and produce conversion diagnostics.

String properties accept a number or boolean whose string form satisfies the
property's string constraints, so `MinSize: 1` passes `=~"^[0-9]+$"` and
`Enabled: true` passes an unconstrained string. The string form is CUE's
interpolation of the value (`1`, `-1`, `1.5`, `true`), which can differ from
CloudFormation's for exponents and trailing zeros. A string enum member such as
`"-1"` or `"true"` accepts the number or boolean spelled the same way. When a
property also has a numeric or boolean branch, numbers or booleans must satisfy
that branch instead. Parameter `Default` and `AllowedValues` accept booleans;
other parameter fields keep their string type.

`Fn::If` branches for a string result accept any number or boolean, so `Name:
{"Fn::If": ["C", 2, "x"]}` passes an unconstrained string property, as `Name: 2`
does. Lists, objects and `null` remain invalid string branches, and numeric or
boolean results keep their own branch types. Branches are not checked against
the property's enum or pattern, so a number branch is accepted for an enum
property just as a non-member string branch is; cfn-lint checks each branch.
Function arguments such as `Fn::Join` items and `Fn::Sub` variables keep their
string type, although cfn-lint accepts numbers there.


### Manifest format

`manifest.json` has a top-level `format`, currently 2. A manifest without
`format` is format 1, where the lifecycle fields below are unknown and
`regions` maps each region to its type-to-hash map. Consumers should refuse a
newer format.

`regions` lists the generated regions, sorted. `types` has one entry per
resource type, giving its schema hash and the regions that have it:

```jsonc
"regions": ["af-south-1", "ap-east-1", ...],
"types": {
  "AWS::S3::Bucket":     { "hash": "69ff2c30cdaed778", "regions": "*" },
  "AWS::Bedrock::Agent": { "hash": "24f43d472ecafdba", "exceptRegions": ["af-south-1", ...] },
  "AWS::Foo::Rare":      { "hash": "...", "regions": ["us-east-1", "us-west-2"] }
}
```

`regions` is `"*"` when the type is in every listed region. Otherwise the
entry has either `regions`, the sorted regions that have the type, or
`exceptRegions`, the sorted regions that lack it, whichever list is shorter;
on a tie it has `regions`. `hash` is the type's schema in every one of those
regions; generation requires one schema per type because the per-type
packages are shared by every region. Each `regions/<region>/index.json`
still holds that region's full type-to-hash map.

`schemas.<hash>` describes one resource schema. Besides `type`, `sha256` and
`propertiesRequired`, it has fields derived for semantic validation:

- `attributes`: Fn::GetAtt names, with cfn-lint's per-type overrides.
- `identifierProperties`: the top-level properties a template sets to make up
  the primary identifier (see [primary identifiers](semantic-validation.md#primary-identifiers)).

It also copies these provider schema keywords verbatim, in provider order,
with property paths kept as JSON pointers: `primaryIdentifier`,
`additionalIdentifiers`, `createOnlyProperties`,
`conditionalCreateOnlyProperties`, `writeOnlyProperties`,
`readOnlyProperties`, `deprecatedProperties`, `replacementStrategy`,
`propertyTransform`, `tagging` and `handlers`. A keyword the schema omits is
omitted. `handlers` keeps each handler's `permissions` and `timeoutInMinutes`
and drops `handlerSchema`. None of these keywords affect the generated CUE.

## Importing resource types

To reference a single resource type from authored CUE, import its per-type
package. Every type has one, at `<Org>/<Service>/<Name>`, with the type's last
segment as the package name:

```cue
package example

import s3bucket "github.com/cueformation/cfn-cue/AWS/S3/Bucket"

bucket: s3bucket.#Resource & {
    Type: "AWS::S3::Bucket"
    Properties: {
        BucketName: {"Fn::Sub": "${Name}-bucket"}
        Tags: [s3bucket.#Tag & {Key: "team", Value: "data"}]
    }
}
```

The package re-exports `#Resource` and the provider definitions, such as
`#Tag`, of the type's shared `resources/h<hash>` schema, so the path stays the
same when the schema changes. Loading it imports only that schema and takes
about 15 ms. `Custom::` names have no package of their own: use
`AWS/CloudFormation/CustomResource`, whose `#Resource` accepts
both `AWS::CloudFormation::CustomResource` and `Custom::<Name>`. This is the
recommended way to reference a resource type. For another CUE module, copy or
publish the generated module through CUE's module system before importing it.

Per-type packages are shared by all regions, so importing one does not check
that the type exists in the region you deploy to. The regional `#Template` and
`cfn-cue-validate` check availability. Sharing is possible because every type
uses the same schema in every region where it is available; generation fails
if a source ever maps one type to different schemas in different regions.

To check a whole template, the regional package's `#Template` selects each
resource's schema by `Type`:

```cue
package example

import cf "github.com/cueformation/cfn-cue/regions/us-east-1:cf"

template: cf.#Template & {
    Parameters: Name: Type: "String"
    Resources: Bucket: {
        Type: "AWS::S3::Bucket"
        Properties: BucketName: {"Fn::Sub": "${Name}-bucket"}
    }
    Outputs: BucketArn: Value: {"Fn::GetAtt": ["Bucket", "Arn"]}
}
```

Importing `regions/us-east-1:cf` loads every resource package in the region
(1,839 in us-east-1), which takes about 2 s before any validation starts. The
fast paths are the per-type packages above, and `cmd/cfn-cue-validate`, which
imports only the schemas a template uses and checks each smoke-corpus
template in under 25 ms. A module generated with `-resources` also loads faster.
`cf.#ResourceTypes["AWS::S3::Bucket"]` exposes the same definition as the
per-type package's `#Resource`, at the cost of loading the region.

For lightweight regional validation, import `regions/us-east-1/types:types` and use
its `#Template`. It checks template structure and available resource type names,
leaving resource `Properties` unconstrained. Common template sections retain
their intrinsic validation. The full regional imports above retain property and
intrinsic checking. The two packages are isolated so importing the lightweight
one never imports the full resource registry.

**Scaling:** once loaded, the full package's `#Template` costs about 1 ms per
resource whatever the region's size, because it looks up `#ResourceTypes[Type]`
rather than trying every schema. The full package defines no union of all
resource schemas; unifying with one would cost about 0.5 s per resource in a
complete region. [BENCHMARKS.md](../BENCHMARKS.md) has the measurements.

To record paired measurements at several resource counts:

```sh
./scripts/regions.sh benchmark --cue /path/to/cue \
  --sizes 2,10,50,all --timeout 20 --out .region-performance-alpha3
```

See the [regional schema and performance guide](regional-performance.md)
for YAML validation, repeatable samples, optional template checks, memory and
timeout interpretation, and the manual GitHub performance workflow.

## Recursive intrinsic model

The generator emits a finite graph of named CUE definitions. It does not recursively expand function definitions in Go and does not impose a nesting-depth cutoff.

A shared function table has optional function keys and enforces exactly one key in a concrete expression. Result-type views select the permitted keys for strings, numbers, booleans, lists and objects. Multi-type properties use a combined view so shared functions such as `Ref` do not leave competing alternatives. Generic objects reserve intrinsic keys, which prevents malformed functions from passing as ordinary objects. Common input types keep the recursive graph small. `Fn::If` branches use the expected result type, with the scalar compatibility described above for strings; `Ref: "AWS::NoValue"` works through the dynamic reference alternative. Logical functions enforce their argument counts without constructing an eagerly expanded recursive tuple.

`Fn::If` can also choose a resource's whole `Properties`, `UpdatePolicy` or `CreationPolicy`. Each `Properties` branch must match the resource's properties schema, be `Ref: "AWS::NoValue"`, or be another `Fn::If` with the same branch rules, to any depth. A hidden recursive definition in `#Resource` (`_#propertiesIf`) holds this rule; it recurses only through a closed object whose single key is `Fn::If`, so every literal leaf is checked against the exact properties schema rather than an arbitrary object. Custom resource properties are otherwise open, so their top level reserves the `Fn::If` and `Ref` keys; without that, a conditional or `AWS::NoValue` object would also partly match the properties alternative. Policy branches are objects, `AWS::NoValue` or another `Fn::If`. Other intrinsics, such as `UpdatePolicy: {Ref: P}`, are rejected, as in cfn-lint. The schemas treat an `Fn::If` with an `AWS::NoValue` branch as a present value; `cfn-cue-validate` checks the branches that remove a value (see [Conditional removal](novalue.md)).

Local schema references reuse intrinsic alternatives already present in their
target definition. Adding them again at a reference created incomplete values
for object-valued `Fn::If`, including the pinned EMR example. References to
unlifted applicator constraints still receive their own alternatives.

Simple “at least one of these properties is required” alternatives remain
presence predicates. Converting them into a CUE union can leave a branch with
an absent required key unresolved, even when another branch accepts the input.
Exact duplicate normalized `anyOf` alternatives are removed; `oneOf` duplicates
remain because they affect whether exactly one branch matches.

The schema checks call shapes and known result categories. Dynamic functions such as `Ref`, `GetAtt`, `FindInMap` and `Select` remain compatible with multiple result categories because their values depend on template context. LanguageExtensions output requires a transform containing `AWS::LanguageExtensions`; standard output excludes `Fn::Length` and `Fn::ToJsonString`. Resource `Fn::ForEach` fragments have their own recursive definition.

## Validation boundary

This is a structural schema generator, not a replacement for cfn-lint. Keep cfn-lint in the validation workflow for reference return types, function-specific nesting permissions, function placement, transform execution, condition evaluation beyond [conditional removal](novalue.md), cross-resource service relationships, and the AWS service rules not listed under [Service rules from cfn-lint](cfn-lint-rules.md).

A separate semantic pass checks what the schemas leave unresolved: `Ref`
targets, `Fn::GetAtt` resources and attribute names, `Fn::Sub` variables,
`DependsOn`, condition and mapping names, dependency cycles, the
properties where `{{resolve:ssm-secure:...}}` may appear, duplicate IAM
statement `Sid`s, and resources of one type that set the same primary
identifier. It runs
without CUE and never substitutes deployment-time values; a cycle or missing
resource that occurs only for some parameter values is a warning. Select it
with `cfn-cue-validate -checks semantic` or `-checks all`; the default,
`structural`, runs the CUE schemas alone. The generator records each
resource type's GetAtt names and the primary identifier properties a
template sets in `manifest.json` for this pass. See
[semantic validation](semantic-validation.md) for the rules, the
treatment of conditions and unknown values, and known differences from
cfn-lint.

Literal property constraints apply directly to literal values. Intrinsic values receive argument and result-type checks; this version does not evaluate them or specialize their branches with the complete target property's enum, pattern, or nested object schema. Parameter type names and SAM Globals have structural coverage. Listed IAM policy properties use the [IAM policy profiles](iam-policies.md); other policy objects have structural coverage. ForEach expansion and ForEach in Conditions/Outputs remain future work.

`cfn-cue-validate` reports `invalid` when any check it can run fails, even if other resources have types missing from the bundle; those resources are listed as not checked. It reports `unsupported` only when everything checkable passes. Resources are checked in logical-ID order, so repeated runs give the same status and message. The [corpus guide](../corpus/README.md#compare-validators) gives the full policy.

The conversion report in generated/manifest.json lists every remaining approximation. Unsupported Python/ECMAScript regex features are reported; Unicode `\uXXXX` escapes are translated to RE2 notation. Provider patterns with lookarounds, which RE2 lacks, become equivalent RE2 patterns where the lookaround's position is fixed: `^(?!aws:)[a-z:]+$` must match `^[a-z:]+$` and must not match `^(?:aws:)`. Of 145 such patterns in the snapshot, 123 are translated and checked against ECMAScript; the rest stay reported. See [provider patterns with lookarounds](regex-lookarounds.md). Selected-key uniqueness degrades to whole-item uniqueness. Other unsupported constraints are reported rather than silently presented as enforced. AWS provider annotations and defaults are not emitted as deployable values.

Fourteen cfn-lint identifier formats are enforced as additional literal
patterns: VPC, subnet, security group, AMI and KMS key IDs, KMS alias, security
group, S3 bucket and CloudWatch Logs log group names, plus IAM role, KMS key,
SNS topic, ACM certificate, and Lambda function ARNs. cfn-lint's bucket name pattern uses lookarounds, which
RE2 lacks, so it is checked as two equivalent patterns. `ipv4-network` and
`ipv6-network` use `fn.#IPv4Network` and `fn.#IPv6Network`, which reject a
network with host bits set, such as `192.168.1.0/0`, as Python's `ipaddress`
does for cfn-lint. An IPv4 network can also use a dotted netmask or hostmask,
such as `10.0.0.0/255.0.0.0`, as in Python; the mask must be contiguous and the
address must have no host bits set for it. A prefix length with leading zeros,
such as `/08`, is still rejected. See the [format policy](format-policy.md).
The `.Ids` and `.Names` list formats annotate arrays; they remain unenforced
and listed in the manifest, while their item formats apply. cfn-lint has no
rule for them. cfn-lint never checks `AWS::Logs::LogGroup.Name` because of a
registration bug; it is enforced here with cfn-lint's intended pattern, which
matches the CloudWatch Logs API, except where a provider schema admits other
names. Existing provider patterns still apply.
Unresolved intrinsic values defer their result format checks.
LaunchTemplate's `resolve:ssm` ImageId exception is scoped to that property.

A string, number or boolean property, or one whose schema has no type, also
accepts a string containing an `ssm`, `ssm-secure` or `secretsmanager` dynamic
reference (`fn.#DynamicReference`), as cfn-lint does. CloudFormation resolves
the reference before it checks the property, so the provider's pattern, enum,
length and format constraints don't apply to it. Object and list properties
don't accept one. As in cfn-lint, the reference must be on the first line, which
may be followed by one final newline. The semantic pass checks that `ssm-secure`
appears only in supported properties (`ssm-secure-placement`); other placement
and syntax checks remain with cfn-lint. Embedded IAM
policy semantics remain separate.
