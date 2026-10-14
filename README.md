# cfn-cue

A working Go generator for regional CloudFormation CUE schemas, built with CUE's Go AST and JSON Schema extractor. This is a new implementation alongside the 2021 generator in [cue-sh/cfn-cue](https://github.com/cue-sh/cfn-cue).

The included snapshot produces **45 full regional template packages, 45 lightweight type-name packages, and 1,866 shared resource schemas**, covering **58,816 region/resource bindings**. Every generator run emits both regional representations. Identical input schemas are emitted once and referenced by the full regional packages. SAM's 13 additional schemas are available with `-include-sam`.

## Run

Requires Go 1.26 or later. The CUE library is pinned to [v0.18.0-alpha.3](https://github.com/cue-lang/cue/releases/tag/v0.18.0-alpha.3). Generated modules declare CUE language v0.18.0; intrinsic argument tuples retain their fixed-length semantics.

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

`-source` accepts a local ZIP, a directory containing `providers/` and `resources/`, or an HTTPS ZIP URL. `-module` sets the generated CUE module path. `-strict` refuses to publish output if conversion diagnostics identify omitted or approximated upstream constraints. Strict mode covers schema conversion; semantic CloudFormation checks remain separate. `-iam-policies=false` keeps provider schemas for IAM policy properties (see [IAM policies](#iam-policies)).

Output is generated in a staging directory. A failed run leaves the previous output intact. Only directories containing this generator's manifest marker may be replaced. Treat the entire generated directory as disposable; put authored templates elsewhere.

## Reproduce the tests

Requires Go 1.26+ and Python 3.12+. These are the same entry points used in CI:

```sh
./scripts/corpus.sh setup
./scripts/corpus.sh test
```

`test` runs Go checks, the pinned S3/IAM integration, parser tests, and all 92
offline corpus expectations. To repeat the downloaded-example investigation:

```sh
./scripts/corpus.sh fetch
./scripts/corpus.sh full
./scripts/corpus.sh diagnose
```

The full survey uses the original six-second deadline and writes
`.corpus-full-report/report.md`. `resume` continues an interrupted full run;
`sample` checks 50 examples. See the [corpus guide](corpus/README.md) for each
profile, report locations, prerequisites, and advanced options. The
`cmd/cfn-cue-validate` Go tool imports only resource schemas used by each input.

## Generated module

```text
generated/
  cue.mod/module.cue
  fn/functions.cue                 Intrinsic function definitions
  template/template.cue            Shared template sections and resource envelope
  iam/iam.cue                      IAM policy grammar and per-placement profiles
  literal/h<hash>/schema.cue        #Properties for literal resource data
  resources/h<hash>/schema.cue      #Properties with intrinsics, plus #Resource
  regions/<region>/region.cue      #Template, #Resources, #Resource, #ResourceTypes
  regions/<region>/types/region.cue #Template, #Resources, #Resource, #ResourceType
  manifest.json                   Source checksum, availability, conversion report
```

The two resource representations serve different purposes. `literal/` preserves resource data shapes, including read-only properties. `resources/` describes inputs to a template: it removes read-only properties and permits expressions at value boundaries. Required fields, closed objects, numeric limits, enums, arrays, local references and schema applicators are handled by the CUE JSON Schema extractor. Presence rules between properties are emitted as CUE directly, so errors name the fields. This covers mutually exclusive properties and exactly-one-of or at-least-one-of groups (for example `CidrIpv6 cannot be used with CidrIp; set exactly one of CidrIp, CidrIpv6, ...`). Groups that name several properties per alternative, or names that aren't capitalized identifiers, keep the extractor's `matchN` form.

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

Inside the generated module, an authored CUE template can import a regional package:

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

For another CUE module, copy or publish the generated module through CUE's module system before importing it. `cf.#ResourceTypes["AWS::S3::Bucket"]` exposes a specific regional definition. Read the manifest to locate and import its shared hash package.

For lightweight regional validation, import `regions/us-east-1/types:types` and use
its `#Template`. It checks template structure and available resource type names,
leaving resource `Properties` unconstrained. Common template sections retain
their intrinsic validation. The full regional imports above retain property and
intrinsic checking. The two packages are isolated so importing the lightweight
one never imports the full resource registry.

**Scaling:** the full package's `#Template` selects each resource's schema by
`Type` (`#ResourceTypes[Type]`), so validation costs about 1 ms per resource
whatever the region's size. Loading the complete us-east-1 package (1,839 types)
takes about 2.7 s, because every resource package is imported; `-resources`
modules load faster. The lightweight package checks structure and type names
without loading property schemas. [BENCHMARKS.md](BENCHMARKS.md) has the
measurements.

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

A shared function table has optional function keys and enforces exactly one key in a concrete expression. Result-type views select the permitted keys for strings, numbers, booleans, lists and objects. Multi-type properties use a combined view so shared functions such as `Ref` do not leave competing alternatives. Generic objects reserve intrinsic keys, which prevents malformed functions from passing as ordinary objects. Common input types keep the recursive graph small. `Fn::If` branches use the expected result type; `Ref: "AWS::NoValue"` works through the dynamic reference alternative. Logical functions enforce their argument counts without constructing an eagerly expanded recursive tuple.

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

This is a structural schema generator, not a replacement for cfn-lint. Keep cfn-lint in the validation workflow for reference existence, attribute names and return types, `Sub` variables, function-specific nesting permissions, function placement, transform execution, condition evaluation, cross-resource relationships, and AWS service rules.

Literal property constraints apply directly to literal values. Intrinsic values receive argument and result-type checks; this version does not evaluate them or specialize their branches with the complete target property's enum, pattern, or nested object schema. Parameter type names and SAM Globals have structural coverage. Listed IAM policy properties use the profiles described below; other policy objects have structural coverage. ForEach expansion and ForEach in Conditions/Outputs remain future work.

The conversion report in generated/manifest.json lists every remaining approximation. Unsupported Python/ECMAScript regex features are reported; Unicode `\uXXXX` escapes are translated to RE2 notation. Selected-key uniqueness degrades to whole-item uniqueness. Other unsupported constraints are reported rather than silently presented as enforced. AWS provider annotations and defaults are not emitted as deployable values.

Eight cfn-lint identifier formats are enforced as additional literal patterns:
VPC, security group, and AMI IDs, plus IAM role, KMS key, SNS topic, ACM
certificate, and Lambda function ARNs. Existing provider patterns still apply.
Unresolved intrinsic values and template dynamic references defer their result
format checks. LaunchTemplate's `resolve:ssm` ImageId exception is scoped to
that property. Dynamic references still receive existing provider constraints;
placement and syntax checks remain with cfn-lint. Embedded IAM policy semantics
remain separate.

## IAM policies

`iam/iam.cue` defines the IAM policy grammar and one profile per policy kind. In `resources/` schemas, the generator replaces the provider type of these properties with a profile:

| Profile | Properties |
|---|---|
| `#TrustPolicyValue` | `AWS::IAM::Role.AssumeRolePolicyDocument` |
| `#IdentityPolicyValue` | inline `Policies[].PolicyDocument` on IAM Role, User and Group; `PolicyDocument` on IAM Policy, ManagedPolicy, RolePolicy, UserPolicy and GroupPolicy; `AWS::SSO::PermissionSet.InlinePolicy` |
| `#S3BucketPolicyValue` | `AWS::S3::BucketPolicy.PolicyDocument` |
| `#KMSKeyPolicyValue` | `KeyPolicy` on `AWS::KMS::Key` and `AWS::KMS::ReplicaKey` |
| `#ServiceControlPolicyValue`, `#ResourceControlPolicyValue` | `AWS::Organizations::Policy.Content` when `Type` is `SERVICE_CONTROL_POLICY` or `RESOURCE_CONTROL_POLICY` |

The profiles check which elements each policy kind allows. Trust policies need a principal and take no `Resource`. Identity policies take no `Principal`. SCP `Allow` statements may only name `Resource: "*"`. Customer-managed RCPs only deny, with `Principal: "*"`. They also check `Version`, the `Action` and ARN shapes, condition operators, and the exactly-one rules for `Action`/`NotAction` and the other element pairs. Every leaf also accepts an intrinsic, and statements can be `Fn::If` or `AWS::NoValue`. The profiles don't check whether an action, resource or condition key exists; leave that to IAM Access Analyzer or cfn-lint.

A placement that no longer matches the provider schema is reported as an `iamPolicy` diagnostic. `literal/` schemas keep provider types. Known limits with CUE v0.18.0-alpha.3:

- `Fn::If` over a whole `Statement` list is rejected; use `Fn::If` per statement.
- A policy written as a literal JSON string is checked with `json.Validate`, which skips the exactly-one rules.
- A failing policy often produces a vague error such as `Statement: field not allowed`. To find the failing field, unify the statement with its statement profile, for example `iam.#TrustStatement & {...}`.

Other templates can import `iam` and unify profiles with their own values, or add organization rules on top.

## Checks

```sh
go test ./...
go vet ./...

# After generating the full included bundle:
CFN_CUE_INTEGRATION=1 go test ./internal/generate \
  -run TestCurrentResources -timeout 3m

# Benchmark the complete us-east-1 aggregate with a bounded run.
GOMEMLIMIT=2GiB CFN_CUE_INTEGRATION=1 CFN_CUE_FULL_REGION=1 \
  go test ./internal/generate -run TestCurrentResources -timeout 3m

# Per-resource cost against each resource's own schema.
CFN_CUE_PERF=1 go test ./internal/generate -run TestResourcePerformance -v

# Union versus Type-selection cost at several region sizes (see BENCHMARKS.md).
scripts/region-bench.sh
```

Tests cover recursive provider definitions, nested intrinsics, conditional branches, wrong return categories, malformed function objects, required properties, unknown fields/types, read-only filtering, schema exclusivity, LanguageExtensions, resource ForEach, regional selection, strict diagnostics, and preservation of existing output after failure. The opt-in test generates a filtered module from the real upstream snapshot and checks S3 and IAM templates, including policy documents that permit either strings or objects.

See [RESEARCH.md](RESEARCH.md) for upstream locations and the next design decisions.

## License

The generator is licensed under [Apache-2.0](LICENSE). Bundled AWS schema data and generated definitions derived from it use the upstream [MIT-0 license](snapshots/LICENSE.aws); see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
