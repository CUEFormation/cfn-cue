# Semantic reference validation

The generated CUE schemas check a template's structure: sections, resource
properties, and the shape and result class of each intrinsic function. They
do not know which names a template declares. `{"Ref": "Missing"}` is a
well-formed `Ref`, so the schemas accept it. The corpus survey found templates
that pass the schemas and fail cfn-lint for this reason
([corpus/reports/2026-10-14](../corpus/reports/2026-10-14/README.md)).

The semantic pass in `internal/semantic` covers that gap for symbols and
references. It is plain Go over the decoded JSON template. It does not load
CUE, call AWS, or execute macros, and it never substitutes a deployment-time
value.

## Invocation

`cfn-cue-validate` selects checks with `-checks`:

| `-checks` | What runs | Notes |
| --- | --- | --- |
| `structural` (default) | Generated CUE schemas | Unchanged results; existing contracts use this mode |
| `semantic` | Reference checks only | Reads the manifest for regional availability, GetAtt names and primary identifiers; no CUE evaluation |
| `all` | Both | Semantic diagnostics are reported even when the schemas reject the template |

```sh
go run ./cmd/cfn-cue-validate -schemas generated -region us-east-1 \
  -template template.json -checks all
```

The result JSON gains `checks` and `diagnostics`. Each diagnostic has a
`rule`, a `severity` (`error` or `warning`), a JSON pointer `path` into the
template, a `message`, and, for warnings that depend on conditions, the
`conditions` involved. Errors make the status `invalid`. Warnings never do.
When the schemas accept a template and the semantic pass rejects it, `phase`
is `semantic`. When both reject it, the message is the schema error and the
semantic diagnostics are still listed.

Semantic checks need the whole template, so they can't be combined with
`-resource`. Templates the validator already reports as unsupported (SAM and
other macros, `Fn::ForEach`, unavailable types) never reach the pass.

From Go, `semantic.Check(template, semantic.Options{Attributes: ..., PrimaryIdentifier: ...})`
returns the diagnostics. `semantic.Analyze` returns the `Model`, the symbol table and
reference list that the checks use, for callers that add their own rules.

The corpus harness passes `--checks` through to the validator:

```sh
.venv/bin/python corpus/corpus.py run --corpus corpus/semantic --checks all \
  --limit 0 --regions us-east-1,us-west-2 --resources none \
  --expect corpus/semantic/expectations.json --report .corpus-semantic-report
```

## Rules

| Rule | Severity | Checks | cfn-lint counterpart |
| --- | --- | --- | --- |
| `ref-undefined` | error | `Ref` names a parameter, resource or pseudo parameter | E1020 |
| `ref-context` | error | `Conditions` and `Rules` refer only to parameters and pseudo parameters, not resources or `GetAtt` | E1020, E8003 |
| `getatt-undefined` | error | `Fn::GetAtt` names a resource, not a parameter | E1010 |
| `getatt-attribute` | error | The attribute is one the resource type returns, for `Fn::GetAtt` and dotted `Fn::Sub` variables | E1010 |
| `sub-undefined` | error | Each `${Name}` is a parameter, resource, pseudo parameter or listed variable | E1019 |
| `dependson-undefined` | error | `DependsOn` names a resource | E3005 |
| `condition-undefined` | error | Resource and output `Condition`, `Fn::If` and `Condition` functions name a declared condition | E3015, E1028 |
| `condition-cycle` | error | Conditions don't refer to themselves | none |
| `mapping-undefined`, `mapping-key` | error | `Fn::FindInMap` names a mapping, and its literal keys exist unless a `DefaultValue` is given | E1011 |
| `duplicate-logical-id` | error | A name isn't both a parameter and a resource | E3007 |
| `dependency-cycle` | error or warning | Resources don't depend on themselves through `Ref`, `GetAtt`, `Sub` or `DependsOn` | E3004 |
| `conditional-target` | error or warning | A reference to a resource with a `Condition` is evaluated only where that resource exists | W1001, E3005 |
| `ssm-secure-placement` | error | `{{resolve:ssm-secure:...}}` appears only in the 11 resource properties CloudFormation resolves it in, such as `AWS::RDS::DBInstance` `MasterUserPassword` | E1027 |
| `duplicate-sid` | error or warning | Statements of one identity, trust or KMS key policy don't repeat a literal `Sid` | E3510, E3530, E3512 |
| `duplicate-identifier` | error or warning | Two resources of one type don't set the same literal primary identifier | E3019 |

`${!Literal}` is an escape, and whitespace inside `${ Name }` is ignored, as in
cfn-lint. Variables bound in `Fn::Sub`'s second argument shadow template names.

`ssm-secure-placement` checks every section except the template's
`Metadata`, as cfn-lint does. It also copies E1027's blind spots, which
CloudFormation itself would still reject:

- It skips strings inside intrinsic functions other than `Fn::If`, such as
  `Fn::Sub` and `Fn::Join`. Condition functions such as `Fn::Equals` are
  checked.
- It checks `Fn::If` branches only when the condition is defined.
- It reads only single-line strings.
- In a string with several dynamic references, it checks only the last one.

### Resource attributes

The generator records each schema's `Fn::GetAtt` names in
`manifest.json` (`schemas.<hash>.attributes`), following cfn-lint v1.57.2's
`_getatts.py`:

- Read-only properties, with nested pointers joined by dots
  (`/properties/Endpoint/Address` becomes `Endpoint.Address`).
- cfn-lint's list of writable properties that GetAtt also returns, such as
  `AWS::AppMesh::Mesh.MeshName`.
- For about 60 types, such as `AWS::EC2::Instance`, every property path. Arrays
  of objects or arrays are not addressable.
- `Outputs.*` for nested stacks and provisioned products, and `*` for custom
  resources.

cfn-lint reads its own patched copy of the provider schemas, so its list can
differ from the bundled snapshot.

### Primary identifiers

Each provider schema names the properties that identify a resource
(`primaryIdentifier`). Two resources of one type with the same identifier
can't both be created: CloudFormation creates the first and the second fails,
although each is valid by itself. The generator records the identifier's
property names in `manifest.json` (`schemas.<hash>.identifierProperties`),
following cfn-lint v1.57.2's `PrimaryIdentifiers.py` (E3019):

- Only identifiers a template sets are recorded. If any part is read-only,
  such as `VpcId` on `AWS::EC2::VPC` or `QueueUrl` on `AWS::SQS::Queue`,
  CloudFormation assigns it and the field is omitted.
- Every part must be a top-level property. Nested pointers are omitted.
- `AWS::CodeBuild::Project` is identified by a read-only `Id`, but cfn-lint
  checks `Name` instead, and so does the generator.

The manifest's `primaryIdentifier` is the provider's list of pointers,
without these rules applied.

Composite identifiers, such as `RestApiId` and `StageName` on
`AWS::ApiGateway::Stage`, collide only when every part is equal. The check
compares literal values only. A missing part is generated by CloudFormation,
so it can't collide, and an intrinsic such as `Fn::Sub` stays unresolved even
when two resources use the same expression. `Fn::If` around `Properties` or
around an identifier value is followed: each literal branch is compared under
its condition, and `AWS::NoValue` counts as missing. A resource with more than
64 such alternatives is not compared; expansion stops as soon as the bound is
passed. Scalars compare as strings, and `"true"`/`"false"` match booleans in
any letter case, as in cfn-lint. Numbers compare as written in the template,
so `1` matches `"1"` and large integers keep every digit, but `1` and `1.0`
differ, where cfn-lint compares two numbers by value. The diagnostic is on the
later resource in name order, names the earlier one and shows the value that
collides; an error that combines `Fn::If` branches lists each value.

## Unknown values

The pass has three kinds of value:

- **Literal**: a string, number or boolean in the template.
- **Unresolved**: anything decided at deployment. This includes parameters
  (defaults can be overridden), pseudo parameters, resource attributes,
  `Fn::ImportValue`, dynamic references and every other intrinsic result.
  `AWS::Region` stays unresolved even though the validator runs for a region,
  because a template is validated once and deployed to many regions.
- **Conditional**: a value inside an `Fn::If` branch, or anything in a
  resource or output with a `Condition`.

Checks that need an unresolved value skip it rather than guess:

- `Fn::GetAtt` with an attribute name computed by `Ref` doesn't check the
  attribute.
- `Fn::Sub` with a computed template string checks only its variable map.
- `Fn::FindInMap` checks the mapping name but not a computed key.
- A resource type without attribute data skips the attribute check. That
  covers a manifest written before attributes were recorded, or a type absent
  from it.

### Conditions

Every reference records its **guards**: the `Condition` of the resource or
output that contains it, and the `Fn::If` branch on each enclosing path
(`Guard{Condition, Value}`). `Conditions.Classify(guards)` decides whether a
set of guards can hold together without choosing parameter values:

| Result | Meaning |
| --- | --- |
| `Always` | True for every value of the unresolved inputs |
| `Never` | False for every value |
| `Sometimes` | Depends on unresolved inputs |
| `Unknown` | More than 16 unresolved comparisons; not decided |

Conditions compile into boolean formulas. `Fn::Equals` over two literals is
evaluated, and an expression compared with itself is true. Any other comparison
is an unresolved atom. The truth table over those atoms is enumerated, with
two constraints the template itself states:

- One expression can't equal two different literals at once. `Env == "prod"`
  and `Env == "dev"` exclude each other.
- A parameter with `AllowedValues` equals one of them. `Env == "test"` is false
  when `test` isn't allowed. If the formula compares the parameter with every
  allowed value, one comparison holds.

Undefined and cyclic conditions become unresolved atoms, so a reported mistake
doesn't also hide or invent a result elsewhere.

CloudFormation resolves conditions before it orders resources, so a reference
in an untaken branch adds no dependency. Templates rely on this to make a
dependency conditional with `Fn::If` in `Metadata`. The condition-aware
rules therefore distinguish certain failures from possible ones:

- `dependency-cycle` is an error when every edge of the cycle is present for
  all condition values. A cycle that exists only for some parameter values is
  a warning that lists its guards. A cycle whose guards contradict each other,
  such as `Fn::If` taking opposite branches of one condition, is not reported.
- `conditional-target` is an error when the referenced resource is absent
  everywhere the reference is evaluated, for every parameter value. Otherwise
  it is a warning. `Fn::If: [C, {Ref: R}, AWS::NoValue]` with `R` guarded by `C`
  is not reported.
- `duplicate-identifier` combines both resources' `Condition`s and the `Fn::If`
  branches that chose each value. It is an error when the two resources share
  the identifier for every parameter value, a warning that lists the guards
  when they share it only for some values, and not reported when they never
  coexist with it. `Fn::If: [Prod, a, b]` in both resources is an error, since
  each branch collides.
- `duplicate-sid` is an error when the policy's existence implies both
  statements do, or that some list chosen by a whole-`Statement` `Fn::If`
  repeats the Sid. A repeat that exists only for some condition values is a
  warning, and statements that never exist together, such as opposite
  branches of one condition, are not reported.

cfn-lint ignores conditions when it looks for cycles and reports `E3005` for
any `DependsOn` on a conditional resource. For `E3019` it skips resources whose
conditions can't hold together, but reports an error, not a warning, when they
can. The semantic corpus records these
divergences explicitly.

### IAM statement IDs

IAM rejects a policy whose statements repeat a `Sid`, including the empty
string. The rule checks the policy properties the generator gives the
identity, trust and KMS key profiles (see [IAM policies](iam-policies.md)).
cfn-lint checks the same uniqueness in identity policies (E3510), trust
policies (E3530) and resource policies (E3512). E3512 also covers SNS, SQS and
OpenSearch policies, which have no profile here, and doesn't cover
`AWS::KMS::ReplicaKey`, which this rule checks. Neither checks S3 bucket
policies or Organizations policies: S3 accepts repeated Sids, and an AWS
example template repeats one. A test keeps the rule's property list in step
with the generator's.

The rule reads a policy written as an object or as a JSON string. It follows
`Fn::If` around the policy, around `Statement`, and around each statement,
and skips `AWS::NoValue` entries. A `Sid` given by an intrinsic is not
compared. A whole-`Statement` `Fn::If` chooses one list, so its two branches
are checked separately, and its condition counts toward the statements'
conditions, not the policy's.

Each list entry is present when any of its `Fn::If` branches with the `Sid`
is, so `[S, {"Fn::If": [C, S, S]}]` always repeats the Sid. Two branches of
one entry are never compared with each other. Earlier entries count
together, so `[{"Fn::If": [C, S, N]}, {"Fn::If": [C, N, S]}, S]` always
repeats the Sid at the third entry. A diagnostic points to the first pair of
branches that can exist together, and a warning lists their conditions.

Checking this in the CUE profiles was also tried: Sids became labels of a
hidden struct, so a repeat failed unification. It worked but added about 5% to
corpus validation time, and its error was a conflict between two positions
buried in disjunction output. The Go rule takes about 5 ms across the corpus
and names both statements.

## Corpus coverage

`corpus/semantic/` holds 53 project-authored cases, generated by
`corpus/make_semantic.py` and checked in two regions with `--checks all`. Each
rule has valid and invalid cases. `scripts/corpus.sh test` runs the contract.
`scripts/corpus.sh regenerate-semantic` rebuilds it; review the diff, because
expectations are written explicitly and never baselined automatically.

Nine cases intentionally disagree with cfn-lint:

| Case | Validator | cfn-lint | Why |
| --- | --- | --- | --- |
| `cycle-conditional` | valid (warning) | E3004 | The cycle exists only when a parameter-dependent condition holds |
| `cycle-exclusive-conditions` | valid (warnings) | E3004, E3005 | The two resources never coexist, so there is no cycle |
| `conditional-target-dependson` | valid (warning) | E3005 | The target is absent only for some parameter values |
| `conditional-target-never` | invalid | valid (W1001) | The target's condition is a false literal comparison, so the reference always fails |
| `condition-cycle` | invalid | valid | cfn-lint has no circular-condition check |
| `sid-statement-branch` | valid (warning) | E3510 | Only one branch of a whole-`Statement` `Fn::If` repeats a Sid, and the branch depends on a parameter |
| `identifier-conditional` | valid (warning) | E3019 | The two log groups collide only when a parameter-dependent condition holds |
| `identifier-intrinsic` | valid | E3019 | The names are the same `Fn::Sub` expression, which the pass doesn't resolve |
| `identifier-number-spelling` | valid | E3019 | The names are the numbers `1` and `1.0`, which the pass compares as written |

### Full survey

The pass was run in `-checks semantic` mode over all 1,101 downloaded
templates in us-east-1. Its results were joined with the pinned cfn-lint
verdicts from the structural survey in `corpus/reports/2026-10-14`. That
survey's lint results don't depend on the validator, so the join attributes
every change to the semantic pass.

- 65 templates have semantic errors, and cfn-lint reports errors for all of
  them. The pass rejects nothing that cfn-lint accepts. 27 `conditional-target`
  warnings don't change any result.
- 23 of the 60 templates the schemas accepted and cfn-lint rejected become
  rejected by both: undefined `Ref`, `GetAtt` and `Sub` names, cycles,
  undefined conditions, a duplicate logical ID and a misspelled KMS key attribute.
- In two templates, cfn-lint rejects for other reasons and doesn't report an
  undefined condition name that the pass finds.
- cfn-lint reports reference errors that the pass doesn't in 11 templates:
  result-type errors (such as a `Ref` where an object is required), which are
  outside this pass, and `E3005` on conditional `DependsOn` targets, which the
  pass reports as warnings.

A complete run through the harness is
`./scripts/corpus.sh full --checks all --report .corpus-full-semantic`.

`duplicate-identifier` was surveyed separately over the extended corpus of
2,383 templates, with `-checks all` in us-east-1, comparing each status with
the previous validator:

- 14 templates change from valid to invalid, such as checkov's
  `AthenaWorkgroupConfiguration-FAIL.yaml`, where two `AWS::Athena::WorkGroup`
  resources set `Name: MyCustomWorkGroup`. cfn-lint reports `E3019` and no
  other error in each of them. No other status or diagnostic changes.
- 25 templates have `duplicate-identifier` diagnostics, and cfn-lint reports
  `E3019` for the same resources in all of them. cfn-lint's
  `bad/resources/primary_identifiers.yaml` gets errors and three warnings
  where its conditions depend on a parameter; the matching `good` fixture has
  none.
- cfn-lint reports `E3019` in three more templates, which the schemas already
  reject. They repeat one `Fn::Sub` or `Fn::Join` expression, which the pass
  doesn't compare. It also reports `E3019` in one SAM template, which the
  validator reports as unsupported.

## Boundary

The semantic pass covers names and dependencies. These remain with cfn-lint:

- `Ref` and `GetAtt` result types against the property that receives them, and
  `Fn::GetAZs`, `Fn::Select` and `Fn::Split` results.
- Function nesting and placement, such as `Ref: AWS::NoValue` outside `Fn::If`.
- Warning-level rules: unused parameters, conditions and mappings, and output
  export names.
- Service rules that relate resources, described below.
- SAM, `AWS::LanguageExtensions` expansion and other macros, which the
  validator reports as unsupported.

Structural checks remain usable without this pass. `-checks structural` is the
default, and the CUE schemas and regional packages don't depend on it.

## Extending to service rules

Rules such as "an ECS service's task definition must use `awsvpc` if the
service sets `NetworkConfiguration`", or "an API Gateway stage's deployment
must belong to the same REST API", need the value of a property in another
resource. They build on the model this pass provides:

1. **Resolve a reference to a resource, not a value.** `TaskDefinition:
   {Ref: TaskDef}` resolves to the logical ID `TaskDef` and its literal
   properties. A parameter or imported ARN stays unresolved, and the rule
   doesn't apply.
2. **Carry guards through property reads.** The walker already tracks guards
   on every path. A rule reads a property as a list of (value, guards)
   alternatives: one per `Fn::If` branch, with `AWS::NoValue` as absence.
3. **Report by `Classify`.** A violation is an error only when its combined
   guards are `Always`; otherwise it is a warning that names the conditions,
   or nothing when they are `Never`.

Condition-aware required-property checks (issue #11, PR #24) landed in
parallel with their own condition model, `internal/conditions`. It makes the
same core choices as `semantic.Conditions`: literal comparisons are decided,
comparisons of one expression with different literals exclude each other,
`AllowedValues` restrict a parameter, and undefined conditions are unknown. The
two differ in which parameter types `AllowedValues` restrict, in malformed and
cyclic conditions, in query size limits and in number spelling. The two are
kept separate because they serve different callers.
[conditions.md](conditions.md) lists the differences and how to keep the
shared choices in step.
