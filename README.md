# cfn-cue

A working Go generator for regional CloudFormation CUE schemas, built with CUE's Go AST and JSON Schema extractor. This is a new implementation alongside the 2021 generator in [cue-sh/cfn-cue](https://github.com/cue-sh/cfn-cue).

The included snapshot produces **45 full regional template packages, 45 lightweight type-name packages, and 1,866 shared resource schemas**, covering **58,816 region/resource bindings**. Each resource type also has its own import package. Every generator run emits both regional representations and the per-type packages. Identical input schemas are emitted once and referenced by the full regional packages. SAM's 13 additional schemas are available with `-include-sam`.

## Run

Requires Go 1.26 or later. The CUE library is pinned to [v0.18.0-rc.1](https://github.com/cue-lang/cue/releases/tag/v0.18.0-rc.1). Generated modules declare CUE language v0.18.0; intrinsic argument tuples retain their fixed-length semantics.

```sh
git clone https://github.com/CUEFormation/cfn-cue.git
cd cfn-cue
```

```sh
# Reproduce the included output offline after fetching Go dependencies.
go run ./cmd/cfn-cue -source snapshots/schemas-2026-10-12.zip -out generated

# Fetch the current upstream bundle; defaults to all available regions/types.
go run ./cmd/cfn-cue -out generated

# Generate a smaller module for experimentation.
go run ./cmd/cfn-cue -source snapshots/schemas-2026-10-12.zip \
  -out sample -regions us-east-1,us-west-2 \
  -resources AWS::S3::Bucket,AWS::EC2::Instance

# Enable LanguageExtensions functions and resource ForEach fragments.
go run ./cmd/cfn-cue -source snapshots/schemas-2026-10-12.zip \
  -out extended -language-extensions

# Include region-independent SAM types.
go run ./cmd/cfn-cue -source snapshots/schemas-2026-10-12.zip \
  -out sam -include-sam
```

`-source` accepts a local ZIP, a directory containing `providers/` and `resources/`, or an HTTPS ZIP URL. `-module` sets the generated CUE module path. `-strict` refuses to publish output if conversion diagnostics identify omitted or approximated upstream constraints. Strict mode covers schema conversion; semantic CloudFormation checks remain separate. `-iam-policies=false` keeps provider schemas for IAM policy properties (see [IAM policies](#iam-policies)). `-cfn-lint-rules=false` omits the service rules taken from cfn-lint (see [Service rules from cfn-lint](#service-rules-from-cfn-lint)).

Output is generated in a staging directory. A failed run leaves the previous output intact. Only directories containing this generator's manifest marker may be replaced. Treat the entire generated directory as disposable; put authored templates elsewhere.

## Reproduce the tests

Requires Go 1.26+ and Python 3.12+. These are the same entry points used in CI:

```sh
./scripts/corpus.sh setup
./scripts/corpus.sh test
```

`test` runs Go checks, the pinned S3/IAM integration, parser tests, all 192
offline corpus expectations, and the 106 semantic reference expectations. To
repeat the downloaded-example investigation:

```sh
./scripts/corpus.sh fetch
./scripts/corpus.sh full
./scripts/corpus.sh diagnose
```

The full survey uses the original six-second deadline and writes
`.corpus-full-report/report.md`. `resume` continues an interrupted full run;
`sample` checks 50 examples. See the [corpus guide](corpus/README.md) for each
profile, report locations, prerequisites, and advanced options. The
`cmd/cfn-cue-validate` Go tool reads the region's `index.json` and imports only
resource schemas used by each input. Semantic checks also read `manifest.json`.

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
  the primary identifier (see [primary identifiers](docs/semantic-validation.md#primary-identifiers)).

It also copies these provider schema keywords verbatim, in provider order,
with property paths kept as JSON pointers: `primaryIdentifier`,
`additionalIdentifiers`, `createOnlyProperties`,
`conditionalCreateOnlyProperties`, `writeOnlyProperties`,
`readOnlyProperties`, `deprecatedProperties`, `replacementStrategy`,
`propertyTransform`, `tagging` and `handlers`. A keyword the schema omits is
omitted. `handlers` keeps each handler's `permissions` and `timeoutInMinutes`
and drops `handlerSchema`. None of these keywords affect the generated CUE.

### Importing resource types

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
complete region. [BENCHMARKS.md](BENCHMARKS.md) has the measurements.

To record paired measurements at several resource counts:

```sh
./scripts/regions.sh benchmark --cue /path/to/cue \
  --sizes 2,10,50,all --timeout 20 --out .region-performance-alpha3
```

See the [regional schema and performance guide](docs/regional-performance.md)
for YAML validation, repeatable samples, optional template checks, memory and
timeout interpretation, and the manual GitHub performance workflow.

## Recursive intrinsic model

The generator emits a finite graph of named CUE definitions. It does not recursively expand function definitions in Go and does not impose a nesting-depth cutoff.

A shared function table has optional function keys and enforces exactly one key in a concrete expression. Result-type views select the permitted keys for strings, numbers, booleans, lists and objects. Multi-type properties use a combined view so shared functions such as `Ref` do not leave competing alternatives. Generic objects reserve intrinsic keys, which prevents malformed functions from passing as ordinary objects. Common input types keep the recursive graph small. `Fn::If` branches use the expected result type, with the scalar compatibility described above for strings; `Ref: "AWS::NoValue"` works through the dynamic reference alternative. Logical functions enforce their argument counts without constructing an eagerly expanded recursive tuple.

`Fn::If` can also choose a resource's whole `Properties`, `UpdatePolicy` or `CreationPolicy`. Each `Properties` branch must match the resource's properties schema, be `Ref: "AWS::NoValue"`, or be another `Fn::If` with the same branch rules, to any depth. A hidden recursive definition in `#Resource` (`_#propertiesIf`) holds this rule; it recurses only through a closed object whose single key is `Fn::If`, so every literal leaf is checked against the exact properties schema rather than an arbitrary object. Custom resource properties are otherwise open, so their top level reserves the `Fn::If` and `Ref` keys; without that, a conditional or `AWS::NoValue` object would also partly match the properties alternative. Policy branches are objects, `AWS::NoValue` or another `Fn::If`. Other intrinsics, such as `UpdatePolicy: {Ref: P}`, are rejected, as in cfn-lint. The schemas treat an `Fn::If` with an `AWS::NoValue` branch as a present value; `cfn-cue-validate` checks the branches that remove a value (see [Conditional removal](#conditional-removal-with-awsnovalue)).

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

This is a structural schema generator, not a replacement for cfn-lint. Keep cfn-lint in the validation workflow for reference return types, function-specific nesting permissions, function placement, transform execution, condition evaluation beyond [conditional removal](#conditional-removal-with-awsnovalue), cross-resource service relationships, and the AWS service rules not listed under [Service rules from cfn-lint](#service-rules-from-cfn-lint).

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
[semantic validation](docs/semantic-validation.md) for the rules, the
treatment of conditions and unknown values, and known differences from
cfn-lint.

Literal property constraints apply directly to literal values. Intrinsic values receive argument and result-type checks; this version does not evaluate them or specialize their branches with the complete target property's enum, pattern, or nested object schema. Parameter type names and SAM Globals have structural coverage. Listed IAM policy properties use the profiles described below; other policy objects have structural coverage. ForEach expansion and ForEach in Conditions/Outputs remain future work.

`cfn-cue-validate` reports `invalid` when any check it can run fails, even if other resources have types missing from the bundle; those resources are listed as not checked. It reports `unsupported` only when everything checkable passes. Resources are checked in logical-ID order, so repeated runs give the same status and message. The [corpus guide](corpus/README.md#compare-validators) gives the full policy.

The conversion report in generated/manifest.json lists every remaining approximation. Unsupported Python/ECMAScript regex features are reported; Unicode `\uXXXX` escapes are translated to RE2 notation. Provider patterns with lookarounds, which RE2 lacks, become equivalent RE2 patterns where the lookaround's position is fixed: `^(?!aws:)[a-z:]+$` must match `^[a-z:]+$` and must not match `^(?:aws:)`. Of 145 such patterns in the snapshot, 123 are translated and checked against ECMAScript; the rest stay reported. See [provider patterns with lookarounds](docs/regex-lookarounds.md). Selected-key uniqueness degrades to whole-item uniqueness. Other unsupported constraints are reported rather than silently presented as enforced. AWS provider annotations and defaults are not emitted as deployable values.

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
such as `/08`, is still rejected. See the [format policy](docs/format-policy.md).
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
may be followed by one final newline. Placement and syntax checks, such as
`ssm-secure` only in supported properties, remain with cfn-lint. Embedded IAM
policy semantics remain separate.

## Conditional removal with AWS::NoValue

`{"Ref": "AWS::NoValue"}` removes the property, list entry or `Properties` object that holds it. The generated schemas can't see the template's `Conditions`, so they accept `AssumeRolePolicyDocument: {"Fn::If": ["IsProd", {...}, {"Ref": "AWS::NoValue"}]}` as present. `cfn-cue-validate` adds a check for this case. After the template passes the schemas, it finds every position in each resource's `Properties` that `AWS::NoValue` can remove, directly or through nested `Fn::If`. For each reachable combination of conditions, it checks a copy of the resource with those positions deleted against the resource schema. A required property removed in any reachable combination is reported with the resource, the removed path, and the condition values that select the branch:

```text
Resources.Role: AWS::NoValue removes Properties.AssumeRolePolicyDocument when condition IsProd is false:
Resources.Role.Properties.AssumeRolePolicyDocument: field is required but not present
```

The same copies catch required fields in nested objects (`Properties.Policies[0].PolicyDocument`), a removed `Properties` object for a type with required properties, and at-least-one groups whose members are all removed. Only errors that name a removed position as a required field are reported. Optional properties and list entries can be removed freely. If the template already fails the schemas, that error is reported instead.

Reachability uses the template's `Conditions` and `Parameters` (`internal/conditions`). The semantic pass has its own condition model; [docs/conditions.md](docs/conditions.md) compares the two:

- `Fn::And`, `Fn::Or`, `Fn::Not` and `Condition` are evaluated as written. A branch is reachable only if some assignment satisfies every enclosing `Fn::If` choice and the resource's `Condition` attribute. A resource whose `Condition` can never hold is never created, so its branches aren't checked.
- `Fn::Equals` of two literals is a known boolean, compared by string form (`"1"` equals `1`). An expression compared with literals takes at most one of them, so `Env = prod` and `Env = dev` can't both hold. A `Ref` to a `String` or `Number` parameter with `AllowedValues` takes only those values.
- Every other comparison, such as two different `Ref`s or a `Fn::FindInMap` result, is an unknown boolean, independent of the rest.
- An undeclared, cyclic or malformed condition is unknown, not true or false. Both of its branches stay reachable. Each use of the name has the same value, so a resource with `Condition: X` can't reach `X`'s false branch. Undeclared names themselves are left to cfn-lint.

Compared with cfn-lint 1.57.2 on the IAM Role cases in `internal/validate/novalue_test.go`, both report one branch, either branch, both branches, nested conditionals, and the resource's own `Condition` the same way. The model is stricter about what is unreachable than cfn-lint in three cases, so it accepts templates that cfn-lint rejects:

- a constant condition (`Fn::Equals: [a, a]`; cfn-lint warns W8003 and checks both branches);
- different literal comparisons with a pseudo parameter, such as two `AWS::Region` values;
- a resource whose `Condition` is constantly false.

It also reports a `Properties` object that `Fn::If` replaces with `AWS::NoValue` when the type has required properties. cfn-lint accepts that, but CloudFormation can't create the resource without them.

Remaining approximations:

- Values are not resolved, so comparisons involving `Fn::FindInMap`, `Fn::Select`, `Fn::Join` and similar functions don't constrain each other. They may make an unreachable branch look reachable.
- A query over more than 16,384 assignments of the underlying comparisons is undecided. If a resource has more than 32 distinct removal sets, or its combinations are undecided, each removal is checked with the others the same choices force. That fallback misses failures that need independent removals together, and an undecided removal is not reported.
- Other rules are not checked against removals. A list that `AWS::NoValue` leaves shorter than `minItems`, or a cross-property rule that a removal breaks, is not reported. The copies keep every other `Fn::If` as a present value, which may not match any single condition assignment. In the corpus, the widdix DynamoDB template removes a `KeySchema` sort key while keeping conditional local indexes; checking that rule would reject a template cfn-lint accepts.
- `AWS::NoValue` inside other intrinsic arguments, `Fn::ForEach` output, and resource attributes other than `Properties` are not checked. Branches are not otherwise specialized: a kept `Fn::If` value is still checked only for its result type.
- The check runs in `cfn-cue-validate` and `internal/validate`. Importing `regions/<region>:cf` and unifying a template with `#Template` in CUE does not include it.

## Service rules from cfn-lint

cfn-lint adds service rules to the provider schemas as JSON Schema "extension" files. The generator applies 49 of them, copied unchanged from cfn-lint v1.57.2 into `internal/generate/cfnlint/extensions/`, to 34 resource types. Examples: an S3 bucket with a public `AccessControl` needs `OwnershipControls`; a Lambda target group can't set a port; `ZipFile` code needs a Node.js or Python runtime; an Aurora instance can't set `AllocatedStorage`; a Fargate task needs a supported `Cpu` and `Memory` combination. `internal/generate/extensions.go` lists each rule ID, file and property path.

Only error-level rules that check one resource are used. Rules that look at other resources, list instance types per region, or carry cfn-lint's multi-megabyte RDS instance class tables remain with cfn-lint, as do warnings.

Most rules become CUE validators that re-check the whole resource, so their cost grows with the resource. Seven rules with several conditions on large resources are instead compiled to comprehensions that read only the fields they name (`internal/generate/scoped_rules.go`): the ECS task definition rules (E3042, E3047, E3048), the DynamoDB billing mode rules (E3638, E3639) and the RDS engine version rules (E3690, E3691). A failure names the rule, for example `E3638: when BillingMode is PAY_PER_REQUEST, ProvisionedThroughput capacity units must be 0`; a property a rule requires reports `field is required but not present`. As validators, these seven added up to 457 ms to a corpus template and 34% to the total CUE time of the 101 templates that use them. Compiled this way, they add a median of 1 ms, at most 10 ms, and 3% in total. [corpus/reports/2026-10-14-scoped-rules](corpus/reports/2026-10-14-scoped-rules/README.md) has the measurements.

The generator rewrites the files to approximate cfn-lint’s behavior, with the following compatibility rules and limits:

- `const` and `enum` compare loosely, as in cfn-lint: `"0"` equals `0`, and `"true"` equals `true`.
- A numeric condition such as `maximum: 60` is not met by a string. cfn-lint compares `"45"` as a number, so CUE skips some rules it would apply, but never applies one to a numeric string cfn-lint exempts.
- An intrinsic never satisfies a rule's condition, so `AccessControl: {Ref: Acl}` doesn't require `OwnershipControls`. cfn-lint instead resolves the `Ref` to the parameter's values, so it can report a rule CUE skips. In the seven compiled rules this also holds under `not`: `BillingMode: {Ref: Mode}` doesn't require `ProvisionedThroughput`.
- Outside conditions, the seven compiled rules accept an intrinsic or dynamic reference wherever they constrain a value, and compare a numeric string as a number, as cfn-lint does: `ReadCapacityUnits: "0"` fails E3639. cfn-lint reports E3691 for `EngineVersion: "{{resolve:ssm:/name}}"`; CUE accepts it.
- cfn-lint compares Python's string form, so the YAML float `EngineVersion: 8.0` matches `"8.0"` and the integer `8` doesn't. The compiled rules match this; validator rules match neither.
- `^(?!X).*$` lookahead patterns become a negated prefix, and the RDS rules that lowercase `Engine` match it case-insensitively. A rule with any other pattern RE2 can't compile is skipped with a diagnostic, because dropping a pattern from a condition would apply the rule too widely. Provider-pattern lookaround translation is not used for conditions.
- `not` over required properties becomes `false` property schemas, and `anyOf` in a condition becomes a double `not`. CUE treats a missing required field in a disjunction as incomplete rather than failing, which would otherwise make these rules reject every resource of the type or always apply.

A rule whose path ends at a referenced definition is added to that definition, so it applies wherever the definition is used, while cfn-lint applies it only at the listed path. Conditions in `Fn::If` values aren't evaluated. These rules are approximations of cfn-lint's, not AWS documentation; disable them with `-cfn-lint-rules=false`.

## IAM policies

`iam/iam.cue` defines the IAM policy grammar and one profile per policy kind. In `resources/` schemas, the generator replaces the provider type of these properties with a profile:

| Profile | Properties |
|---|---|
| `#TrustPolicyValue` | `AWS::IAM::Role.AssumeRolePolicyDocument` |
| `#IdentityPolicyValue` | inline `Policies[].PolicyDocument` on IAM Role, User and Group; `PolicyDocument` on IAM Policy, ManagedPolicy, RolePolicy, UserPolicy and GroupPolicy; `AWS::SSO::PermissionSet.InlinePolicy` |
| `#S3BucketPolicyValue` | `AWS::S3::BucketPolicy.PolicyDocument` |
| `#KMSKeyPolicyValue` | `KeyPolicy` on `AWS::KMS::Key` and `AWS::KMS::ReplicaKey` |
| `#ServiceControlPolicyValue`, `#ResourceControlPolicyValue` | `AWS::Organizations::Policy.Content` when `Type` is `SERVICE_CONTROL_POLICY` or `RESOURCE_CONTROL_POLICY` |

The profiles check which elements each policy kind allows. Trust policies need a principal and take no `Resource`. Identity policies take no `Principal`. SCP `Allow` statements may only name `Resource: "*"`. Customer-managed RCPs only deny, with `Principal: "*"`. They also check `Version`, the `Action` and ARN shapes, condition operators, and the exactly-one rules for `Action`/`NotAction` and the other element pairs. Every value also accepts an intrinsic, which is not evaluated: an intrinsic `Effect` skips the SCP `Resource` rule, for example. `Statement`, a single statement, `Principal` or `Condition` can be `Fn::If`, and a statement, `Condition` or the whole policy can be removed with `AWS::NoValue`. A whole-policy `Fn::If` may nest another one in either branch. Condition keys may use OIDC issuer namespaces such as `token.actions.githubusercontent.com:sub`. The profiles don't check whether an action, resource or condition key exists; leave that to IAM Access Analyzer or cfn-lint.

A placement that no longer matches the provider schema is reported as an `iamPolicy` diagnostic. `literal/` schemas keep provider types. Known limits with CUE v0.18.0-rc.1:

- A policy written as a literal JSON string is checked with `json.Validate`, which skips the exactly-one rules.
- Repeated statement `Sid`s are not checked here; the semantic pass reports them (see [semantic validation](docs/semantic-validation.md#iam-statement-ids)).
- A failing policy often produces a vague error such as `Statement: field not allowed`. To find the failing field, unify the statement with its statement profile, for example `iam.#TrustStatement & {...}`.

Other templates can import `iam` and unify profiles with their own values, or add organization rules on top.

## Checks

```sh
go test ./...
go vet ./...

# After generating the full included bundle:
CFN_CUE_INTEGRATION=1 go test ./internal/generate \
  -run TestCurrentResources -timeout 3m

# IAM Role AWS::NoValue cases, and the smoke corpus against the complete
# us-east-1 and us-west-2 #Template.
CFN_CUE_INTEGRATION=1 go test ./internal/validate \
  -run 'TestIAMRoleNoValue|TestCorpusRegionalTemplates' -timeout 3m

# Benchmark the complete us-east-1 aggregate with a bounded run.
GOMEMLIMIT=2GiB CFN_CUE_INTEGRATION=1 CFN_CUE_FULL_REGION=1 \
  go test ./internal/generate -run TestCurrentResources -timeout 3m

# Per-resource cost against each resource's own schema.
CFN_CUE_PERF=1 go test ./internal/generate -run TestResourcePerformance -v

# Union versus Type-selection cost at several region sizes (see BENCHMARKS.md).
scripts/region-bench.sh
```

Tests cover recursive provider definitions, nested intrinsics, conditional branches, required properties removed by reachable `AWS::NoValue` branches, condition reachability, wrong return categories, malformed function objects, required properties, unknown fields/types, read-only filtering, schema exclusivity, LanguageExtensions, resource ForEach, regional selection, strict diagnostics, and preservation of existing output after failure. The opt-in test generates a filtered module from the real upstream snapshot and checks S3 and IAM templates, including policy documents that permit either strings or objects. A second opt-in test checks the IAM Role `AWS::NoValue` cases compared with cfn-lint. A third checks the 96 smoke corpus templates against the complete us-east-1 and us-west-2 `#Template` and compares the results with `corpus/smoke/expectations.json`.

See [RESEARCH.md](RESEARCH.md) for upstream locations and the next design decisions.

## Agent skills

The project includes [unslop](.agents/skills/README.md) for editing writing. Invoke
it with `$unslop` in Codex or `/unslop` in Claude Code and Cursor.

## License

The generator is licensed under [Apache-2.0](LICENSE). Bundled AWS schema data and generated definitions derived from it use the upstream [MIT-0 license](snapshots/LICENSE.aws); see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
