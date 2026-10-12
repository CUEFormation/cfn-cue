# cfn-cue

A working Go generator for regional CloudFormation CUE schemas, built with CUE's Go AST and JSON Schema extractor. This is a new implementation alongside the 2021 generator in [cue-sh/cfn-cue](https://github.com/cue-sh/cfn-cue).

The included snapshot produces **45 regional template packages and 1,866 shared resource schemas**, covering **58,816 region/resource bindings**. Identical input schemas are emitted once and referenced by regional packages. SAM's 13 additional schemas are available with `-include-sam`.

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

`-source` accepts a local ZIP, a directory containing `providers/` and `resources/`, or an HTTPS ZIP URL. `-module` sets the generated CUE module path. `-strict` refuses to publish output if conversion diagnostics identify omitted or approximated upstream constraints. Strict mode covers schema conversion; semantic CloudFormation checks remain separate.

Output is generated in a staging directory. A failed run leaves the previous output intact. Only directories containing this generator's manifest marker may be replaced. Treat the entire generated directory as disposable; put authored templates elsewhere.

## Generated module

```text
generated/
  cue.mod/module.cue
  fn/functions.cue                 Intrinsic function definitions
  template/template.cue            Shared template sections and resource envelope
  literal/h<hash>/schema.cue        #Properties for literal resource data
  resources/h<hash>/schema.cue      #Properties with intrinsics, plus #Resource
  regions/<region>/region.cue      #Template, #Resources, #Resource, #ResourceTypes
  manifest.json                   Source checksum, availability, conversion report
```

The two resource representations serve different purposes. `literal/` preserves resource data shapes, including read-only properties. `resources/` describes inputs to a template: it removes read-only properties and permits expressions at value boundaries. Required fields, closed objects, numeric limits, enums, arrays, local references and schema applicators are handled by the CUE JSON Schema extractor.

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

**Scaling limitation:** generation across all regions completes, but evaluating the complete us-east-1 aggregate with CUE v0.18.0-alpha.3 still exceeded the three-minute verification limit, with `GOMEMLIMIT=2GiB` as the Go memory target. Use `-resources` to generate a filtered authoring module, or import individual shared resource packages, while the aggregate representation is improved. The full generated files are supplied for inspection, not as a claim that full-region evaluation is production-ready.

## Recursive intrinsic model

The generator emits a finite graph of named CUE definitions. It does not recursively expand function definitions in Go and does not impose a nesting-depth cutoff.

A shared function table has optional function keys and enforces exactly one key in a concrete expression. Result-type views select the permitted keys for strings, numbers, booleans, lists and objects. Common input types keep the recursive graph small. `Fn::If` branches use the expected result type; `Ref: "AWS::NoValue"` works through the dynamic reference alternative. Logical functions enforce their argument counts without constructing an eagerly expanded recursive tuple.

The schema checks call shapes and known result categories. Dynamic functions such as `Ref`, `GetAtt`, `FindInMap` and `Select` remain compatible with multiple result categories because their values depend on template context. LanguageExtensions output requires a transform containing `AWS::LanguageExtensions`; standard output excludes `Fn::Length` and `Fn::ToJsonString`. Resource `Fn::ForEach` fragments have their own recursive definition.

## Validation boundary

This is a structural schema generator, not a replacement for cfn-lint. Keep cfn-lint in the validation workflow for reference existence, attribute names and return types, `Sub` variables, function-specific nesting permissions, function placement, transform execution, condition evaluation, cross-resource relationships, and AWS service rules.

Literal property constraints apply directly to literal values. Intrinsic values receive argument and result-type checks; this version does not evaluate them or specialize their branches with the complete target property's enum, pattern, or nested object schema. Policy objects, parameter type names, and SAM Globals have structural coverage. ForEach expansion and ForEach in Conditions/Outputs remain future work.

The current conversion report has **2,625 diagnostics**: 2,038 semantic format annotations, 546 regex patterns, 29 pattern-property regexes, 9 selected-key uniqueness constraints and 3 distinct-item limits. Unsupported Python/ECMAScript regex features are reported; Unicode `\uXXXX` escapes are translated to RE2 notation. Selected-key uniqueness degrades to whole-item uniqueness. Other unsupported constraints are reported rather than silently presented as enforced. AWS provider annotations and defaults are not emitted as deployable values.

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
```

Tests cover recursive provider definitions, nested intrinsics, conditional branches, wrong return categories, malformed function objects, required properties, unknown fields/types, read-only filtering, schema exclusivity, LanguageExtensions, resource ForEach, regional selection, strict diagnostics, and preservation of existing output after failure. The opt-in test generates a filtered module from the real upstream snapshot and checks S3 and IAM templates, including policy documents that permit either strings or objects.

See [RESEARCH.md](RESEARCH.md) for upstream locations and the next design decisions.

## License

The generator is licensed under [Apache-2.0](LICENSE). Bundled AWS schema data and generated definitions derived from it use the upstream [MIT-0 license](snapshots/LICENSE.aws); see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
