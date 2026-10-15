# Scoped service rules, 2026-10-14

These results support enabling the seven cfn-lint rules that
[`2026-10-14/`](../2026-10-14/README.md) left disabled: the ECS task
definition rules E3042, E3047 and E3048, the DynamoDB billing mode rules
E3638 and E3639, and the RDS engine version rules E3690 and E3691. They are
now compiled to field-scoped comprehensions (`internal/generate/scoped_rules.go`)
instead of `matchIf` validators. All seven are applied; none remain disabled.

All builds below were generated from the same checked-in schema snapshot,
for us-east-1, after the PR #5 definition-pruning integration:

| Build | Generator | Rules |
| --- | --- | --- |
| disabled | main at `d3a6c2be` | 42 rules; the seven disabled |
| validators | `d3a6c2be` with the seven enabled as `matchIf` validators | 49 |
| scoped | this change | 49; the seven as comprehensions |

Only the four affected resource schemas differ between builds. The
validators build reproduces the original implementation for comparison.
Builds were compared with the same module layout (us-east-1 only), because
loading a 45-region module adds about 25 ms to each validator process.

## Paired timing

`scripts/paired_timings.py` times `cfn-cue-validate` on the 101 corpus
templates that use an affected type, and separately on each of their 141
affected resources (`-resource`). Each of three rounds runs every case once
per build in an order shuffled with seed 7. Values are the validator's
elapsed milliseconds, including package loading. Per-case figures take the
median of the three rounds. These are local measurements on an Apple
M2 under other load, not a cross-platform guarantee.

| 101 templates | disabled | validators | scoped |
| --- | ---: | ---: | ---: |
| Median per template | 40 ms | 45 ms | 40 ms |
| Median paired increase | | 4 ms | 1 ms |
| Largest per-template increase | | 457 ms | 10 ms |
| Total CUE time | | +33.6% | +3.2% |

| 141 resources, median (largest) increase | Resources | validators | scoped |
| --- | ---: | ---: | ---: |
| `AWS::DynamoDB::Table` | 64 | 1 (92) ms | 0 (2) ms |
| `AWS::ECS::TaskDefinition` | 18 | 31.5 (328) ms | 4 (5) ms |
| `AWS::RDS::DBCluster` | 8 | 3 (12) ms | 1 (2) ms |
| `AWS::RDS::DBInstance` | 51 | 2 (33) ms | 2 (4) ms |
| Total CUE time | 141 | +64.8% | +6.9% |

Each build's timings were taken in a separate paired run against the
disabled build: `paired-templates-*.json` and `paired-resources-*.json`.

These runs used CUE v0.18.0-alpha.3. After main moved to v0.18.0-rc.1, the
template run was repeated with rc.1 builds of main and this change
(`paired-templates-scoped-rc1.json`): total CUE time rose 1.0%, with the same
six status changes. That run was on a heavily loaded machine (median 65 ms
per template), so its largest per-case increase, 64 ms, is not comparable.
Re-timing the slowest cases over nine interleaved rounds with alpha.3 and
rc.1 builds gave increases between -9 and +17 ms for both versions.

## What made the rules expensive

A `matchIf(condition, then, else)` validator unifies the whole properties
value with each schema, which evaluates the resource's complete schema
again. E3048 nests validators in list items, and E3047 has eight branches,
so a large Fargate task definition took 500 ms or more. Unifying a single
field with a type, such as `(Cpu & int) != _|_`, has the same problem on a
smaller scale: it evaluates the field's schema, including its intrinsic
function alternatives, again.

The scoped encoding avoids both. Comprehension clauses test field
presence; values are compared (`Cpu == "256"`), matched (`Cpu =~ "^\\d+$"`)
or passed to builtins (`math.MultipleOf`, `list.Contains`); and list rules
iterate over items without unifying them. Each top-level condition is split
into clauses, presence tests first, so an RDS version rule costs little when
`EngineVersion` is absent.

## Corpus outcomes

The full survey of 1,101 templates (six-second deadline) was run with the
disabled and scoped builds; `comparison.md` lists the transitions. Six
templates move from `cue-accepts-lint-rejects` to `both-reject`: five RDS
engine version cases and one DynamoDB table without throughput. There are no
new CUE-only rejections (18 before and after) and no new timeouts or
unsupported results.

At resource level, all seven new rejections in the paired runs are also
reported by cfn-lint: three E3691, two E3690, one E3639, and one E3048 in a
template already rejected for other reasons.

## Differences from cfn-lint

Checked against cfn-lint v1.57.2 on 63 edge-case templates, the scoped rules
agree except where CUE is deliberately more lenient:

- A dynamic reference satisfies the rules, as it does other constraints:
  cfn-lint reports E3691 for `EngineVersion: "{{resolve:ssm:/name}}"`.
- An intrinsic never satisfies a condition, including one under `not`: a
  `Ref` `BillingMode` doesn't require `ProvisionedThroughput`.

A property a rule requires, such as `ProvisionedThroughput` for E3639 or
`NetworkMode` for E3048, reports CUE's `field is required but not present`
rather than the rule ID: an error for an absent field would also hold for
the schema itself, which has no data.
