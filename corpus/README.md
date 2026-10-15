# CloudFormation example corpus

This harness keeps downloadable upstream examples, parser normalization, CUE
validation, and semantic linting as distinct steps. Originals are retained byte
for byte. The source lock pins Git commits, archive checksums, licenses, and path
filters. Each downloaded template records its source URL and original and
normalized SHA-256 checksums.

The locked sources are:

| ID | Source | Templates |
| --- | --- | --- |
| `aws-examples` | AWS's [CloudFormation examples](https://github.com/aws-cloudformation/aws-cloudformation-templates) | 144 |
| `cfn-lint-good` | cfn-lint's [good fixtures](https://github.com/aws-cloudformation/cfn-lint/tree/main/test/fixtures/templates/good) | 95 |
| `cfn-lint-bad` | cfn-lint's [bad fixtures](https://github.com/aws-cloudformation/cfn-lint/tree/main/test/fixtures/templates/bad) | 98 |
| `cfn-lint-snapshot` | cfn-lint's quickstart, integration, and public snapshot templates | 44 |
| `widdix` | [widdix/aws-cf-templates](https://github.com/widdix/aws-cf-templates) | 65 |
| `sam-translator-output` | SAM's [translator output](https://github.com/aws/serverless-application-model/tree/develop/tests/translator/output) for the `aws` partition | 567 |
| `troposphere-examples` | troposphere's [example output](https://github.com/cloudtools/troposphere/tree/main/tests/examples_output) | 88 |

“Good” and “bad” are upstream directory names, not guarantees about standalone
linting with today's schemas. Some fixtures rely on test-specific configuration,
and several bad fixtures deliberately fail to parse. AWS's examples use `.yaml`
as the source of truth; generated JSON copies and unrelated `.yml` configuration
files are excluded. SAM's translator output is CloudFormation produced by the
SAM transform; its error cases are not templates and are excluded. The `aws-cn`
and `aws-us-gov` partition copies are excluded because surveys check commercial
regions. troposphere's output is generated JSON with a `.template` extension.

## Quick start

The readable entry point is [`scripts/corpus.sh`](../scripts/corpus.sh). Run
these two commands from a fresh checkout; CI runs the same commands:

```sh
./scripts/corpus.sh setup
./scripts/corpus.sh test
```

Setup creates `.venv/`, installs dependencies with version and package hashes,
downloads the pinned Go dependencies, and builds the two Go tools. Subsequent
tests use the checked-in schemas and examples; they do not download templates
or newer dependencies. An offline version check rejects stale or manually
upgraded Python packages and points back to setup.

`test` runs formatting checks, Go tests and vet, the pinned S3/IAM integration,
Python parser/provenance tests, and the 46-template contract in two regions. The
contract runs twice: through `cfn-cue-validate`, which loads only the resource
packages a template uses, and against each region's complete `#Template`.
Expected failures remain explicit. The final report is
`.corpus-smoke-report/report.md`; a changed expectation makes the command fail.

To reproduce the larger investigation:

```sh
./scripts/corpus.sh fetch
./scripts/corpus.sh full
./scripts/corpus.sh diagnose
```

| Command | Fixed defaults | Result |
| --- | --- | --- |
| `fetch` | Pinned source archives and normalizer | `.corpus/` data; `.corpus-cache/` archives |
| `sample` | 50 coverage-selected templates; us-east-1; 6-second deadline | `.corpus-sample-report/` |
| `full` | Every template; us-east-1; 6-second deadline | `.corpus-full-report/` |
| `resume` | Same full profile and existing binary | Continues `.corpus-full-report/` |
| `diagnose` | EMR, CloudWatch Agent, SQS FIFO, and EFS examples; 10-second deadline; all 13 resources | `.corpus-resources-report/` |
| `schemas` | Checked-in schema ZIP; all regions and types | `.corpus-generated/` scratch bundle |
| `regenerate` | Downloaded locked examples and authored mutations | Updates `corpus/smoke/`; review the diff |
| `setup-flip`, then `flip` | Hash-locked optional CFN Flip comparison | `.corpus-flip/` |
| `compare BEFORE AFTER` | Identical input, harness, lint runtime, and budget checks | `.corpus-comparison/` |

The example survey is a discovery run and can exit successfully while reporting
disagreements or timeouts. Its results depend on the machine's evaluation speed;
the six-second deadline reproduces the original configuration, not an exact
promise of identical timing or counts. No stacks are deployed.

All paths are relative to the repository root, even when invoking the script
from another directory. Additional options are forwarded to the underlying
harness and can override profile defaults:

```sh
./scripts/corpus.sh full --timeout 10 --report .corpus-full-10s
./scripts/corpus.sh resume --timeout 10 --report .corpus-full-10s
./scripts/corpus.sh sample --regions us-east-1,us-west-2 --limit 10
./scripts/corpus.sh schemas
./scripts/corpus.sh test --schemas .corpus-generated
```

Repeat every override when resuming. The resume command deliberately reuses the
binary from the original run; the harness checks its hash and rejects changed
inputs, dependency versions, or options. Other profiles rebuild with checkout
paths and Git metadata excluded, so identical Go sources and toolchains produce
stable binary hashes. Changing Go source or toolchain requires a new report.

To measure a generator change, retain the full report before changing the code,
regenerate schemas and run the same profile into a second directory, then use:

```sh
./scripts/corpus.sh compare .corpus-before .corpus-after
```

Both report directories must contain `run.json` and `report.json`. The comparison
refuses changed corpus checksums, schema source, harness, lint runtime, selected
cases, or budgets. Validator and generated-schema fingerprints may change.
It records every classification transition in JSON and Markdown; machine load
can still affect timeout outcomes. Keep timeouts separate from accept/reject
disagreements when assessing improvements.

`CORPUS_PYTHON` selects the setup interpreter, `CORPUS_VENV` selects the virtual
environment, and `CORPUS_DIR` selects downloaded data. Defaults are `python3`,
`.venv/`, and `.corpus/`. `./scripts/corpus.sh help` lists the commands. The
underlying commands are documented below for users who want to run stages
individually.

## Individual setup commands

Requires Go 1.26+ and Python 3.12+. Run from the repository root:

```sh
python3 -m venv .venv
.venv/bin/python -m pip install --require-hashes -r corpus/requirements.txt
go build -o bin/cfn-cue-validate ./cmd/cfn-cue-validate
```

The Go library stays pinned to CUE v0.18.0-rc.1. The Python dependency lock pins
cfn-lint 1.57.2 and its transitive packages with hashes. Run the Python harness
using that environment, rather than an independently upgraded global cfn-lint.

## Download and normalize

```sh
.venv/bin/python corpus/corpus.py fetch
```

This downloads each source's pinned ZIP archive into `.corpus-cache/` and writes
`.corpus/index.json`, `.corpus/licenses/`, `.corpus/original/`, and
`.corpus/normalized/`. Downloads and archive members have size limits, checksum
verification, and path checks. Cached archives support offline repetition.

The safe cfn-lint YAML parser expands tags such as `!Ref`, `!Sub`, and `!GetAtt`
into long-form mappings. Each example gets normalized JSON for CUE and long-form
YAML for inspection. YAML dates remain textual scalars, including unquoted
`AWSTemplateFormatVersion: 2010-09-09`. Duplicate keys and parsing errors are
recorded, rather than discarded. Numbers, quoted numbers, booleans, and quoted
booleans retain their distinctions. The long-form YAML must round-trip to the
same normalized JSON.

[CFN Flip is archived and unmaintained](https://github.com/awslabs/aws-cfn-template-flip).
Its optional comparison can check that both parsers produce equivalent values:

```sh
.venv/bin/python -m pip install --require-hashes -r corpus/requirements-flip.txt
.venv/bin/python corpus/corpus.py fetch --compare-flip --out .corpus-flip
```

This uses `clean_up=False`. Opinionated cleanup such as rewriting `Fn::Join` into
`Fn::Sub`, scalar coercion, resource replacement, and filling required properties
would change the test input and are deliberately separate from normalization.
The initial 239-template comparison found two failures involving `Fn::ForEach`
and loop-related intrinsic syntax in CFN Flip; the default parser normalized
all 239 templates successfully.

## Compare validators

```sh
# Deterministic sample maximizing new resource types, intrinsic names, and scales.
.venv/bin/python corpus/corpus.py run --limit 50 --resources none

# Evaluate every downloaded example in one region, retaining a checkpoint journal.
.venv/bin/python corpus/corpus.py run --limit 0 --resources none --report .corpus-full

# Resume exactly the same run after interruption.
.venv/bin/python corpus/corpus.py run --limit 0 --resources none --report .corpus-full --resume

# Compare selected examples across regions and isolate their resource checks.
.venv/bin/python corpus/corpus.py run --limit 10 --regions us-east-1,us-west-2 \
  --resources all --max-resource-checks 0 --report .corpus-resources

# Regional availability and schema variation across the entire generated bundle.
.venv/bin/python corpus/corpus.py run --limit 10 --regions all --resources none
```

`--ids` selects exact inventory IDs; `--source` filters source IDs. `--limit 0`
checks every selected template. The default is 50 templates in `us-east-1`, with
a 30-second deadline and up to 10 isolated resource checks for each failing
template. `--resources none` makes an initial survey substantially faster.
`--max-resource-checks 0` removes that resource-count cap. Skipped resource names
are recorded, and are never counted as checked coverage.

Each CUE evaluation and each lint invocation runs in its own process. The CUE
CLI has an internal deadline, and the harness has an external deadline. A
timeout is recorded and the next input continues. `GOMEMLIMIT` defaults to a
2 GiB **soft target**, not a hard memory limit. `heapMiB` measures live Go heap
at completion, not peak memory. Run in a container or managed runner if a hard
memory bound is necessary. Checks are sequential to avoid multiple expensive
CUE evaluations competing for memory.

The CUE validator reads the bundle manifest, checks regional resource
availability, and builds an AST importing only schemas used in the template.
Concrete template data and schema constraints are loaded together. It uses a
virtual file overlay inside the generated module; it does not modify or copy the
bundle and does not evaluate the full regional union. `-resource LogicalID`
checks an individual resource without template-level constraints. Full-template
validation remains necessary to check template sections and resource names.

Valid `Custom::<name>` resource types use the regional
`AWS::CloudFormation::CustomResource` provider schema. Actual type names are
preserved in the template and result metadata; multiple custom names share one
schema import. The provider's required properties and intrinsic rules still
apply. Missing provider schemas remain unsupported, while malformed custom names
and null/non-string Transform entries are invalid.

When resources fail in different ways, the result doesn't depend on map order.
The validator checks every resource in logical-ID order and keeps all problems.
Status precedence is `error`, then `invalid`, then `unsupported`. A malformed
logical ID, a missing or non-string `Type`, or a malformed custom name makes
the template invalid without schema evaluation. Otherwise the supported
resources and template sections are evaluated even when other resource types
have no schema; unsupported resources still get the generic resource attribute
checks. A failure there is `invalid`, and the message ends with one
`not checked: resource ...` line per unsupported resource. The result is
`unsupported` only when everything checkable passes; its message lists each
missing type. Transform and unexpanded-construct checks run first and are
unchanged.

Tool SHA-256 fingerprints are checked at startup and completion. Between engine
calls, the harness checks file identity, size, nanosecond mtime and ctime to detect
rebuilds without repeatedly reading the validator binary. Each original and
normalized template is content-verified once per record, outside the region loop;
metadata guards detect changes during its evaluations. Changing tools or inputs
still requires a new report, including when resuming.

The lint driver uses the original YAML, a fixed empty configuration, and
mandatory error rules. It disables source-provided cfn-lint configuration, so
downloaded metadata cannot load custom rule code or hide errors. cfn-lint may
perform its built-in SAM/LanguageExtensions processing. Custom macros,
`Fn::ForEach` expansion in CUE, SAM expansion in CUE, nested stack content,
reference resolution, and deployment behavior remain outside this CUE check.
Missing types, transforms, and loops are reported as unsupported. No AWS
credentials are needed; nothing is deployed or fetched from `TemplateURL`.

## Reports and regression contracts

Each report contains:

- `report.md`: totals, scale comparison, and source links for cases needing attention.
- `report.json`: full diagnostics, tool versions, hashes, regions, and coverage.
- `resources.csv`: isolated resource results, times, and completion heap sizes.
- `results.jsonl`: flushed checkpoint records, retained even if interrupted.
- `run.json`: input and tool fingerprints; resuming with changed inputs is refused.

Results distinguish agreement, disagreements in either direction, unsupported
inputs, normalization errors, tool failures, and timeouts. When CUE accepts a
template and cfn-lint's only errors are Lambda runtime lifecycle rules (E2531,
E2533), the result is `cue-accepts-lint-lifecycle-only`. Those rules compare
the runtime's AWS deprecation dates with today's date, so they change over
time without any change to the template. The Markdown report counts these
results but leaves them out of the cases requiring attention. Resource coverage
encountered in a failing template is separate from coverage in accepted
templates. Passing both validators is evidence of compatibility, not proof of
deployability: cfn-lint performs best-effort semantic analysis, and the converter
already records approximated or omitted provider constraints in its manifest.

Discovery runs write reports and exit successfully even when examples fail.
Use `--fail-on-disagreement` to require both validators to accept every input.
Use `--expect` for an exact regression contract that includes intentional
negative examples and documented known disagreements. Missing cases, unexecuted
expectations, or changed results fail the contract; no automatic baseline
updates occur.

The checked-in `smoke/` corpus contains 79 templates: 11 pinned upstream examples
and 68 project-authored cases, with 158 expectations across two regions. Cases
cover scalar compatibility, formats, nested intrinsics, required properties,
enums, unknown/read-only fields, wrong function results and arguments, custom
resource aliases and ServiceToken requirements, and invalid Transform values.
Template grammar cases from the full survey cover logical IDs that start with a
digit, `&` in condition names, `Fn::If` around `Properties` and `UpdatePolicy`,
and boolean mapping values. Dynamic reference cases check that a resolved
password skips AmazonMQ's pattern, while a literal or unknown resolver does not.
Format cases cover a CIDR with host bits set, a KMS key ID and an S3 bucket name.
Service rule cases (`rule-*`) cover rules taken from cfn-lint's extension schemas,
including the condition shapes that needed rewriting for CUE and resources whose
slow rules are skipped.
The read-only property and malformed custom type name disagreements with cfn-lint
remain documented. Expected failures are explicit and annotated.

```sh
.venv/bin/python -m unittest discover -s corpus -v
.venv/bin/python corpus/corpus.py run --corpus corpus/smoke --limit 0 \
  --regions us-east-1,us-west-2 --resources none --timeout 15 \
  --expect corpus/smoke/expectations.json --report .corpus-smoke-report
```

CI runs this small offline corpus after installing pinned dependencies, rather
than relying on network downloads of moving examples. To rebuild it, fetch the
locked corpus and run `corpus/make_smoke.py`. Review source and expectation changes
before committing. Larger surveys remain an explicit local or CI job; increasing
the sample size, region count, deadline, or resource checks increases runtime.

## Follow-up work informed by the corpus

Observed disagreements need focused regression tests before changing schemas.
Initial examples expose CloudFormation's scalar coercions (quoted numbers and
booleans), an incomplete CUE result for some object-valued `Fn::If` branches, and
evaluation timeouts for large combinations of resource schemas. Preserve those
inputs while deciding whether coercion belongs in a schema mode, a typed
validation adapter, or a separate semantic layer. Generic string coercion or
weakened intrinsic arguments would obscure real failures.

Add new sources by recording a full commit, archive checksum, license, and
include/exclude patterns in `sources.lock.json`. Keep generated CDK/SAM examples
in distinct sources, since preprocessing has different semantics. Community
repositories need the same provenance and license checks. Future validation
work can add coverage-driven negative mutations, semantic reference checking,
and targeted resource minimization for slow templates.
