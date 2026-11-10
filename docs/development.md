# Development

How to run the generator, reproduce the tests, and run the opt-in checks and benchmarks.

## Generator options

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

`-source` accepts a local ZIP, a directory containing `providers/` and `resources/`, or an HTTPS ZIP URL. `-module` sets the generated CUE module path. `-strict` refuses to publish output if conversion diagnostics identify omitted or approximated upstream constraints. Strict mode covers schema conversion; semantic CloudFormation checks remain separate. `-iam-policies=false` keeps provider schemas for IAM policy properties (see [IAM policies](iam-policies.md)). `-cfn-lint-rules=false` omits the service rules taken from cfn-lint (see [Service rules from cfn-lint](cfn-lint-rules.md)).

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
`sample` checks 50 examples. See the [corpus guide](../corpus/README.md) for each
profile, report locations, prerequisites, and advanced options. The
`cmd/cfn-cue-validate` Go tool reads the region's `index.json` and imports only
resource schemas used by each input. Semantic checks also read `manifest.json`.

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

# Time and memory budget for ten large IAM managed policies.
CFN_CUE_PERF=1 go test ./internal/generate -run TestIAMPolicyPerformance -v

# Union versus Type-selection cost at several region sizes (see BENCHMARKS.md).
scripts/region-bench.sh
```

Tests cover recursive provider definitions, nested intrinsics, conditional branches, required properties removed by reachable `AWS::NoValue` branches, condition reachability, wrong return categories, malformed function objects, required properties, unknown fields/types, read-only filtering, schema exclusivity, LanguageExtensions, resource ForEach, regional selection, strict diagnostics, and preservation of existing output after failure. The opt-in test generates a filtered module from the real upstream snapshot and checks S3 and IAM templates, including policy documents that permit either strings or objects. A second opt-in test checks the IAM Role `AWS::NoValue` cases compared with cfn-lint. A third checks the 96 smoke corpus templates against the complete us-east-1 and us-west-2 `#Template` and compares the results with `corpus/smoke/expectations.json`.

See [RESEARCH.md](../RESEARCH.md) for upstream locations and the next design decisions.

## Agent skills

The project includes [unslop](../.agents/skills/README.md) for editing writing. Invoke
it with `$unslop` in Codex or `/unslop` in Claude Code and Cursor.
