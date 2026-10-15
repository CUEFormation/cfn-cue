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

Measured on October 14, 2026, on the same machine under heavy unrelated load (load average 28 to 42), so absolute times are about three times the figures above. Both modules contain all 1,839 us-east-1 types: the generator before nested `Properties` conditionals (`d3a6c2b`) and after. `CFN_CUE_PERF_IF_DEPTH` wraps every resource's `Properties` in that many `Fn::If`, with the literal properties innermost and `AWS::NoValue` in each other branch. Each configuration ran three times, interleaved; figures are medians in milliseconds. The earlier generator rejects depth 3, so it has no figure there.

| | before | after |
|---|---:|---:|
| Load region package | 7,946 | 7,714 |
| 10 resources, no `Fn::If` | 33 | 41 |
| 50 resources, no `Fn::If` | 267 | 177 |
| 10 resources, depth 1 | 55 | 56 |
| 50 resources, depth 1 | 536 | 545 |
| 10 resources, depth 3 | — | 118 |
| 50 resources, depth 3 | — | 569 |
| 50 resources, depth 3, one broken | — | 745 |

Load time and unconditional or single-level validation are unchanged within noise. Three levels cost 11 to 12 ms per resource, between one and two times a single level, in the worst case where every resource is nested.

### Per type, without the region

`TestResourcePerformance` checks a resource against its own `#ResourceTypes` entry. It loads each resource package in 10 to 24 ms and validates one minimal resource in 1 to 3 ms (`AWS::S3::Bucket` is the slowest).

## Findings

- **Pattern-constraint function keys made region evaluation feasible.** At equal region size the union is 460 to 1,400 times faster than with optional-field keys, which took 4.6 s per resource with only 6 types.
- **The union cost about 0.3 ms per resource type for every resource.** In a full region that is about 0.5 s per resource, 31 s for 50 resources.
- **Selecting the schema by `Type` removes the dependence on region size.** About 1 ms per resource for 6 or 1,839 types, valid or broken, with one error naming the problem instead of a `Type` conflict for every type. On 210 templates and edge cases (the corpus smoke templates, `Custom::` names, missing or non-string types, `Fn::ForEach` fragments, in filtered and full regions) it accepted and rejected exactly what the union did.
- **Load time is what remains.** Loading 1,839 resource packages takes 2.7 s before any validation. Loading only the packages a template uses (for example per-service region packages, or a generated module for the template's types), or validating in a long-running process that loads once, would remove it.

## Not measured

- Templates with many or large properties per resource. The per-type benchmark gives a floor of 1 to 3 ms per minimal resource.
- The `cue` command line with the current generator. In-process figures exclude process start-up and CLI module loading.
- Repeated runs and variance.
