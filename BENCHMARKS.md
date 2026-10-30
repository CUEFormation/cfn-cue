# Benchmarks

Measured on October 14, 2026, on an Apple M2 with CUE v0.18.0-alpha.3 and Go 1.26.4. Each figure is a single run, so treat differences under about 20% as noise.

## Method

`TestRegionPerformance` (`internal/generate/perf_test.go`) loads `regions/us-east-1` from a generated module and validates templates against `#Template` through the Go API. Templates hold 1, 10 or 50 minimal resources cycling through six types: `AWS::SQS::Queue`, `AWS::IAM::Role`, `AWS::EC2::Subnet`, `AWS::S3::Bucket`, `AWS::Lambda::Function` and `AWS::DynamoDB::Table`. Each template is also validated with its first resource broken (`QueueName: []`), because a template being edited is often invalid.

`scripts/region-bench.sh` generates us-east-1 modules with 6, 50, 200, 800 and all 1,839 resource types. The six template types come first, then the rest alphabetically. It runs the test against the current generator and, with `OLD_REF`, the generator at an earlier commit. Three generators are compared here:

| Generator | Commit | Resources checked by | Function keys |
|---|---|---|---|
| optional fields | `f01c05f` | union of every type | optional fields (`Ref?: string`) |
| union | `671dded` | union of every type | pattern constraints (`[=~"^Ref$"]: string`) |
| current | this commit | `#ResourceTypes[Type]` | pattern constraints |

`load` is the time to load and build the region package. `per resource` is validation time divided by template size.

```sh
scripts/region-bench.sh                                   # current generator
OLD_REF=671dded scripts/region-bench.sh                   # also the union
OLD_REF=f01c05f TYPES="6 50" SIZES=1,10 RUN_TIMEOUT=300 scripts/region-bench.sh
CFN_CUE_PERF=1 go test ./internal/generate -run TestResourcePerformance -v   # one type at a time
CFN_CUE_PERF_DIR=<module> CFN_CUE_PERF_IF_DEPTH=3 go test ./internal/generate -run TestRegionPerformance -v
```

`scripts/regions.sh benchmark` measures a different comparison through fresh `cue` CLI processes: the full region package against the lighter `types` package, which checks type names only.

## Results

### Validation time per resource (10-resource template, valid)

| Types in region | optional fields | union | current |
|---:|---:|---:|---:|
| 6 | 11 s | 8 ms | 1.9 ms |
| 50 | 20 s ¹ | 21 ms | 1.4 ms |
| 200 | not run | 67 ms | 1.2 ms |
| 800 | not run | 257 ms | 1.2 ms |
| 1,839 | not run | 519 ms | 1.1 ms |

¹ One-resource template; the 10-resource template exceeded the 300 s limit.

### Whole template, full us-east-1 region (1,839 types)

| Template | union, valid | union, one broken | current, valid | current, one broken |
|---|---:|---:|---:|---:|
| 1 resource | 0.74 s | 0.76 s | <1 ms | <1 ms |
| 10 resources | 5.2 s | 5.5 s | 11 ms | 11 ms |
| 50 resources | 31 s | 48 s | 121 ms | 66 ms |

Loading the region package takes about 2.7 s with either generator, so a full edit-validate cycle for 50 resources is about 3 s with the current generator and 34 to 51 s with the union.

### Loading the region package

| Types in region | optional fields | union and current |
|---:|---:|---:|
| 6 | 4.4 s | 26 to 39 ms |
| 50 | 14.8 s | 75 ms |
| 200 | not run | 225 ms |
| 800 | not run | 900 ms |
| 1,839 | not run | 2.7 s |

Inlining each resource's properties schema into `#Resource` raised the full-region load to about 3.2 s (2.8 s before, three runs each). Validation time did not change.

### Nested `Fn::If` around `Properties`

Measured on October 15, 2026, with CUE v0.18.0-rc.1 and Go 1.26.4 on the same machine. Every module holds all 1,839 us-east-1 types and was generated once, then reused for every repetition. "Before" is the generator at `076ccda`; "after" is `e364842`, main with #21 merged. `CFN_CUE_PERF_IF_DEPTH` wraps every resource's `Properties` in that many `Fn::If`, with the literal properties innermost and `AWS::NoValue` in each other branch. One `go test -c` binary from `e364842` ran every configuration with `GOMAXPROCS=4`, one process per variant and depth. Each configuration ran 20 times. Before and after alternated within each iteration, and the order flipped every iteration. Load average was 5 to 9 for most of the run; one spike to 48 in the third iteration came from other work on the machine. Figures are milliseconds: median, then interquartile range (25th to 75th percentile) and min to max. The earlier generator rejects depth 3, so it has no figure there.

| | before | after |
|---|---|---|
| Load region package | 4,754 (4,624–5,064; 4,284–12,227) | 4,790 (4,623–4,994; 4,244–5,643) |
| 10 resources, no `Fn::If` | 20 (19–23; 18–28) | 43 (41–44; 40–57) |
| 10 resources, no `Fn::If`, one broken | 19 (18–21; 17–28) | 42 (41–43; 40–49) |
| 50 resources, no `Fn::If` | 97 (88–112; 85–312) | 175 (170–181; 160–371) |
| 50 resources, no `Fn::If`, one broken | 97 (91–185; 84–495) | 176 (172–188; 163–372) |
| 10 resources, depth 1 | 34 (33–36; 31–86) | 58 (58–66; 54–291) |
| 50 resources, depth 1 | 164 (157–179; 146–374) | 267 (249–345; 227–534) |
| 10 resources, depth 3 | — | 76 (73–79; 67–258) |
| 50 resources, depth 3 | — | 353 (334–530; 305–858) |
| 50 resources, depth 3, one broken | — | 350 (334–440; 308–764) |

The no-`Fn::If` slowdown is real, and larger than the three-run figures showed (20 to 26 ms and 106 to 125 ms; those did not reproduce). With 10 resources, after was slower in all 20 paired iterations, by a median of 23 ms (2.2 times). That gap is six to eight times the interquartile range of either column (3 to 4 ms), and the ranges do not overlap: the slowest of 20 before runs took 28 ms, the fastest after run 40 ms. With 50 resources the median gap is 78 ms (1.8 times), against interquartile ranges of 11 to 24 ms. Occasional outliers above 300 ms in both columns make the min-to-max ranges overlap, and after was slower in 16 of 20 pairs. Load time is unchanged.

The slowdown does not come from nested `Properties` conditionals. Two more interleaved runs (15 and 12 iterations, load average 5 to 11) added modules from the commits between `076ccda` and `e364842`. Medians are in milliseconds, no `Fn::If`, valid templates:

| Generator | Commit | 10 resources | 50 resources |
|---|---|---:|---:|
| before | `076ccda` | 18 / 19 | 87 / 90 |
| + #24, conditional IAM policy documents | `6fbff63` | 43 | 170 |
| + #22, lookaround patterns | `3743119` | 42 | 171 |
| + #20, reference validation (no CUE change) | `4f087b2` | 43 | 171 |
| + #21, nested `Properties` conditionals (after) | `e364842` | 41 | 180 |

The cells for `076ccda` give the first run, then the second. #21 against `4f087b2` changed the paired medians by -3 to +11 ms, with after slower in only 4 to 9 of 15 pairs, at depth 0 and depth 1 alike. #22 against `6fbff63` is likewise within noise. The whole difference arrives with #24, slower in all 12 pairs at 10 resources. That change touches only `iam/iam.cue`: it makes the `Fn::If` alternatives of each policy document value recursive (`_#TrustPolicyIf` and its siblings). Of the six benchmark types, only `AWS::IAM::Role` imports `iam`, and the other five resource packages are identical across the two commits. The earlier measurement attributed this to nested `Properties` because its "after" commit already included #24.

A follow-up run on October 16, 2026, set the number of roles directly. A throwaway program, not checked in, loaded each module once per process and ran one discarded warm-up template. It then validated each template three times in random order, with no `Fn::If`. Ten processes per generator ran interleaved, with `GOMAXPROCS=4` and load average 4 to 6. Each role has the same literal trust policy as the benchmark role. Figures are median milliseconds over 30 validations, with the interquartile range:

| Template | before (`076ccda`) | #24 (`6fbff63`) | after (`e364842`) | after − before |
|---|---:|---:|---:|---:|
| 20 queues, no roles | 24.4 (24.1–27.2) | 25.0 (24.1–28.5) | 24.9 (23.9–27.8) | +0.4 |
| 2 roles, 18 queues | 26.1 (25.0–27.5) | 50.0 (46.9–53.0) | 48.3 (47.1–50.5) | +22 |
| 5 roles, 15 queues | 26.9 (26.1–29.1) | 70.5 (68.8–77.9) | 73.5 (69.8–88.2) | +47 |
| 10 roles, 10 queues | 27.9 (27.2–29.4) | 114 (108–122) | 112 (109–145) | +85 |
| 20 roles | 30.6 (29.1–32.5) | 196 (184–206) | 192 (187–216) | +161 |
| 40 roles | 59.0 (57.8–65.4) | 405 (369–501) | 388 (368–552) | +329 |
| 2 roles, 8 queues | 13.7 (13.2–14.1) | 37.6 (35.1–52.2) | 35.5 (34.7–39.5) | +22 |
| 2 roles, 48 queues | 63.2 (61.5–75.0) | 86.9 (82.7–106) | 89.8 (83.6–102) | +27 |
| 50 of the other five types, no roles | 89.0 (86.9–95.2) | 89.5 (86.5–103) | 89.5 (85.8–95.4) | +0.6 |
| 10 roles with one inline policy, 10 queues | 38.0 (35.9–39.3) | 206 (196–224) | 207 (197–297) | +169 |

The extra time is linear in the number of roles: about 8.2 ms per role on #24 and after (a fit through the medians of the 20-resource rows), against 0.3 ms per role more than a queue before. It does not move with the number of other resources: 2 roles cost the same 22 to 27 ms more alongside 8 or 48 queues. With no roles there is no difference. An inline `Policies` document (`#IdentityPolicyValue`) on each role about doubles the added cost, to 17 ms per role. So the cost belongs to each IAM policy document a template contains, not to resources in general.

Over the same templates without `Fn::If`, one level of nested `Properties` adds about 1.8 ms per resource at the median (267 against 175 ms for 50 resources), and three levels add about 3.6 ms (353 ms), in the worst case where every resource is nested.

### Per type, without the region

`TestResourcePerformance` checks a resource against its own `#ResourceTypes` entry. It loads each resource package in 10 to 24 ms and validates one minimal resource in 1 to 3 ms (`AWS::S3::Bucket` is the slowest).

### Conditional IAM policy documents

Measured on October 16 and 17, 2026, with CUE v0.18.0-rc.1 under unrelated load (load average 6 to 13). #24 let a policy document property be a nested `Fn::If` over documents. Its encoding matched `"Fn::If"` as a regular field, and CUE evaluated that field's recursive branch types for every literal document, about 8 ms per policy document. This generator matches the key with a pattern (`[=~"^Fn::If$"]`), which applies only when the key is present.

Three modules with all 1,839 us-east-1 types were compared: before #24 (`076ccda`), main before this change (`e364842`), and main with this change. A small Go program loaded `regions/us-east-1` once per process and validated templates of `AWS::IAM::Role` resources with a one-statement trust policy, filled out with `AWS::SQS::Queue`. Scenarios ran in random order, three times per process, in 8 interleaved processes per module: 24 measurements per scenario. The two conditional trust policy rows also ran in a second batch of 8 processes with this change, so its figures there pool 48. Figures are medians in milliseconds.

| | before #24 | main | this change |
|---|---:|---:|---:|
| 20 resources, no roles | 24 | 24 | 24 |
| 20 resources, 10 roles | 27 | 110 | 23 |
| 20 resources, 20 roles | 30 | 186 | 22 |
| 40 roles | 57 | 366 | 42 |
| 10 roles with an inline policy, 10 queues | 36 | 194 | 28 |
| 10 roles with a depth-1 conditional trust policy, 10 queues | 30 | 117 | 33 |
| 10 roles with a depth-3 conditional trust policy, 10 queues | — | 240 | 50 |
| 50 resources of the other five benchmark types | 86 | 87 | 87 |

Each role added 0.25 ms before #24, 7.9 ms on main, and nothing measurable with pattern keys. The earlier generator rejects depth 3. Removing the recursion while keeping the regular field (three unrolled levels) only halved the cost.

`cfn-cue-validate` accepted and rejected the same 1,092 corpus templates with either module, one process per template including load: 839 valid and 253 invalid. Total time fell from 153 to 143 s; the largest savings were 0.9 to 1.4 s per template. Error messages changed for 5 templates: four no longer dump the expanded recursive document type, and one gains a `MinFields` line.

### Large IAM policies

Measured on October 18, 2026, on an Apple M4 with Go 1.27.1. `TestIAMPolicyPerformance` validates one value holding ten `AWS::IAM::ManagedPolicy` resources. Each has 30 statements of 40 actions, 40 resources and a `Condition`. The CLI rows run the same template through `cfn-cue-validate` and report `elapsedMs` and peak RSS from `/usr/bin/time -l`. Peak heap is sampled every millisecond. Three runs each, which agreed within 5%. These figures used CUE v0.18.0-alpha.3.

| iam profiles | Time | Allocated | Peak heap | CLI time | CLI peak RSS |
|---|---:|---:|---:|---:|---:|
| matchN presence rules (`9bfcb45`) | 2.4 s | 2.3 GiB | 2.1 GiB | 2.5 s | 2.2 GiB |
| presence comprehensions | 0.76 s | 704 MiB | 556 MiB | not run | not run |
| plus matchIf list elements | 0.46 s | 582 MiB | 420 MiB | 0.50 s | 450 MiB |

A template with one policy of 200 one-action statements went from 0.20 s and 167 MiB peak RSS to 0.12 s and 92 MiB through the CLI. The 46 smoke corpus templates, three of which have IAM policy properties, took about 20 ms and 36 MiB each before and after, with the same results.

After the move to CUE v0.18.0-rc.1, the test took 3.0 s, 2.6 GiB allocated and 2.3 GiB peak heap on main (`3e7599d`), and 0.52 s, 661 MiB and 450 to 490 MiB with these profiles, three runs each. The CLI rows were not repeated. On October 30, the profiles measured 0.57 s, 661 MiB and 465 to 495 MiB; accepting dynamic references in list elements, with one regular expression per element, brought them to 0.52 s, 575 MiB and 390 to 410 MiB.

## Findings

- **Pattern-constraint function keys made region evaluation feasible.** At equal region size the union is 460 to 1,400 times faster than with optional-field keys, which took 4.6 s per resource with only 6 types.
- **The union cost about 0.3 ms per resource type for every resource.** In a full region that is about 0.5 s per resource, 31 s for 50 resources.
- **Selecting the schema by `Type` removes the dependence on region size.** About 1 ms per resource for 6 or 1,839 types, valid or broken, with one error naming the problem instead of a `Type` conflict for every type. On 210 templates and edge cases (the corpus smoke templates, `Custom::` names, missing or non-string types, `Fn::ForEach` fragments, in filtered and full regions) it accepted and rejected exactly what the union did.
- **matchN cost a full copy of the statement per alternative.** IAM statements checked "exactly one of Action or NotAction" with `matchN`, which unifies a copy of the value with each alternative. Comprehensions on field presence check the same rule without re-evaluating the statement. They cut validation of large policies by two thirds.
- **Load time is what remains.** Loading 1,839 resource packages takes 2.7 s before any validation. Loading only the packages a template uses (for example per-service region packages, or a generated module for the template's types), or validating in a long-running process that loads once, would remove it.

## Not measured

- Templates with many or large properties per resource, other than IAM policies. The per-type benchmark gives a floor of 1 to 3 ms per minimal resource.
- The `cue` command line with the current generator. In-process figures exclude process start-up and CLI module loading.
- Repeated runs and variance, except for nested `Fn::If` around `Properties`.
