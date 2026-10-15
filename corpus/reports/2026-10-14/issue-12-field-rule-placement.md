# GroundStation field-rule placement failure (issue #12)

One full generation during the grammar work for PR #7 failed with:

```text
field rule 2 was not placed by the JSON Schema extractor
```

for `AWS::GroundStation::DataflowEndpointGroupV2`. No source revision, build or intermediate output was captured. This investigation could not reproduce the failure from committed source. The evidence below identifies the only code path known to produce that exact failure and adds a regression test for it. We recommend closing the issue.

## Inputs and versions

| Item | Value |
|---|---|
| Snapshot | `snapshots/schemas-2026-10-12.zip`, SHA-256 `140973ce07b875af…`, schema date 2026-10-12T20:31:47Z |
| Schema | `resources/a3fc2798aeb3e8b6.json` (`AWS::GroundStation::DataflowEndpointGroupV2`) |
| Builds | `d3a6c2be` (main, with definition pruning from PR #5) and `9fea8fe3` (grammar commit before PR #5 was merged in) |
| Toolchain | Go 1.26.4 darwin/arm64, `cuelang.org/go v0.18.0-alpha.3` |
| Host | Apple M2, macOS 26.6.2 |

## What field rule 2 is

`normalize` registers a field rule for each object with a qualifying `oneOf`, `anyOf`, `requiredXor`, `requiredOr` or `dependentExcluded`. It numbers them in key order, and the numbering restarts for each `extract` call. This schema has four, in both literal and template mode:

| Rule | Location | Rule shape |
|---|---|---|
| 0 | `#/definitions/CreateEndpointDetails` | exactly one of Uplink/Downlink endpoint |
| 1 | `#/definitions/DownlinkDataflowDetails` | at least `AgentConnectionDetails` |
| 2 | `#/definitions/EndpointDetails` | exactly one of Uplink/Downlink endpoint |
| 3 | `#/definitions/UplinkDataflowDetails` | at least `AgentConnectionDetails` |

`EndpointDetails` is referenced only by `/properties/EndpointDetails`, which is in `readOnlyProperties`. Template extraction removes that property, which leaves `EndpointDetails` unreferenced. Rule 2 is the only rule in this schema whose definition becomes unreachable in template mode.

## Behaviour of each build

- **`9fea8fe3` (no pruning).** The CUE extractor emits every `$defs` entry, referenced or not (`constraintAddDefinitions` calls `ensureDefinition` and `schema` for each one). The unreferenced `#EndpointDetails` therefore appears in `resources/` with its rule, and rule 2 is placed.
- **`d3a6c2be` (pruning).** `pruneDefinitions` deletes `EndpointDetails` before extraction and sets `c.rules[2] = nil`. `applyRules` skips nil rules. Generation succeeds and `#EndpointDetails` remains only in `literal/`.

Both builds generate this schema successfully, and both are deterministic.

## Fault injection

The exact failure appears, deterministically, when template extraction loses `EndpointDetails` but rule 2 is still expected. Two single-line edits to `d3a6c2be` produce it:

1. Remove `c.rules[index] = nil` from `pruneDefinitions`.
2. Restore the pre-pruning check in `applyRules` (`if !placed[i]` instead of `if r != nil && !placed[i]`).

PR #5 changed `schema.go` and `rules.go` together. A build that had one half of that change but not the other fails in this way.

Each injection gives:

```text
cfn-cue: template AWS::GroundStation::DataflowEndpointGroupV2: field rule 2 was not placed by the JSON Schema extractor
```

for a focused run and also for a full generation. Under injection 1, only two of the snapshot's 1,879 schemas fail: GroundStation (`a3fc2798…`, rule 2) and `AWS::Events::Connection` (`d0df8596…`, rule 0). Generation processes hashes in sorted order, so GroundStation is always the first failure reported. That matches the original observation: one resource, rule index 2, during a full generation. In both cases the staging directory was discarded and no output was published.

`AWS::Events::Connection` reaches the same state another way. A read-only path below `/properties/AuthParameters` causes read-only removal to inline the `AuthParameters` definition into the property. The definition itself is then unreferenced and pruned, while the inlined copy keeps its own rule.

## Reproduction attempts

None of the following failed, and none produced differing output for the same input and options.

| Method | Build | Runs |
|---|---|---|
| In-process literal + template extraction of the GroundStation schema, comparing a hash of the rendered CUE for each extraction | `d3a6c2be` | 2,000 |
| Same | `9fea8fe3` | 2,000 |
| Same with 8 concurrent workers under `-race` | `d3a6c2be` | 400 (no races reported) |
| Every snapshot schema, literal + template, in a freshly shuffled order each round so CUE's process-wide state differs whenever GroundStation is reached; odd rounds with `LanguageExtensions` | `d3a6c2be` | 40 rounds, 150,320 extractions |
| Same | `9fea8fe3` | 40 rounds, 150,320 extractions |
| Full CLI generation (45 regions), cycling the default, `-language-extensions`, `-include-sam` and both together, with `GOGC` 10/100/400/off and `GOMAXPROCS` 1/2/8, both builds running at once to add CPU contention | `d3a6c2be` | 32 |
| Same | `9fea8fe3` | 32 |
| Focused CLI generation (`-resources AWS::GroundStation::DataflowEndpointGroupV2`) | each build | 100 |

Every full generation at a given build and variant produced a byte-identical tree. The default HEAD tree is identical to the committed `generated/`. Go randomizes map iteration on every `range`, so these repeated runs also covered randomized map order. Generation converts resources sequentially, and the generator has no goroutines.

Code review found nothing else that could leave a marker unplaced for this schema. Our map iterations either sort keys or do not affect the result. `propertiesFirst`, `simplifyApplicators` and `dropValidatorStructs` never remove a marker field. Extension schemas are parsed fresh for each resource, and GroundStation has no cfn-lint extensions.

## Conclusion

Committed source does not fail. Every revision of the pruning work (`531eea85`, `a0753f18`, `d2005c5f`, `f1d3a5a9`) drops rules from pruned definitions and skips nil rules, and the merge `d2daa474` integrated `schema.go` and `rules.go` without manual conflict resolution. No nondeterminism appeared in the generator or in the CUE extractor across the runs above.

The only mechanism found that reproduces the reported resource, rule index and first-failure position is template extraction losing `EndpointDetails` while rule 2 is still expected. That is what an incompletely applied pruning change produces. The grammar work was uncommitted in the main checkout from 10:38 to 21:03 on 2026-10-14, while PR #5 was being developed in a separate worktree. The source state of the failing run is not recoverable, so this cannot be confirmed. It is consistent with all available evidence; an intermittent defect in committed code is not.

`TestReadOnlyRuleDefinitions` pins both affected schemas from the snapshot. It checks that generation succeeds, that the orphaned definitions are absent from `resources/` and present in `literal/`, and that the rules on either side of the dropped index still reject input. Both fault injections make it fail with the original message.

`internal/generate/extraction_stress_test.go` keeps the in-process harnesses, gated by `CFN_CUE_STRESS` and `CFN_CUE_STRESS_ALL`, for use if the failure recurs. If it does, record `go version -m` for the binary, `git status` and `git diff` for the source tree, and the exact command line, then rerun the harness with the failing hash.
