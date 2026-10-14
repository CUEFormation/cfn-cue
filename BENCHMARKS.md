# Benchmarks

Measured on October 14, 2026, on an Apple M2 with CUE v0.18.0-alpha.3 and Go 1.26.4, at commit `3f0319f` (pattern-constraint intrinsics on top of `f01c05f`). Each figure is a single run, so treat differences under about 20% as noise.

## Method

`TestRegionPerformance` (`internal/generate/perf_test.go`) loads `regions/us-east-1` from a generated module and validates templates against `#Template` through the Go API. Templates hold 1, 10 or 50 minimal resources cycling through six types: `AWS::SQS::Queue`, `AWS::IAM::Role`, `AWS::EC2::Subnet`, `AWS::S3::Bucket`, `AWS::Lambda::Function` and `AWS::DynamoDB::Table`. Each template is also validated with its first resource broken (`QueueName: 5`), because a template being edited is often invalid.

`scripts/region-bench.sh` generates us-east-1 modules with 6, 50, 200, 800 and all 1,839 resource types. The six template types come first, then the rest alphabetically. It runs the test against three variants:

| Variant | What it is |
|---|---|
| old | Generator at `f01c05f`: function keys as optional fields (`Ref?: string`). |
| union | Current generator: function keys as pattern constraints (`[=~"^Ref$"]: string`). `#Resources` checks each resource against a union of every type in the region. |
| dispatch | Current generator with `#Resources` rewritten to select `#ResourceTypes[Type]` (`Custom::` types use `AWS::CloudFormation::CustomResource`). A prototype applied after generation; the generator does not emit it yet. |

`load` is the time to load and build the region package. `per resource` is validation time divided by template size.

```sh
scripts/region-bench.sh                       # union and dispatch
OLD_REF=f01c05f TYPES="6 50" SIZES=1,10 RUN_TIMEOUT=300 scripts/region-bench.sh
CFN_CUE_PERF=1 go test ./internal/generate -run TestResourcePerformance -v   # one type at a time
```

`scripts/regions.sh benchmark` measures a different comparison through fresh `cue` CLI processes: the full region package against the lighter `types` package, which checks type names only.

## Results

### Validation time per resource (10-resource template, valid)

| Types in region | old | union | dispatch |
|---:|---:|---:|---:|
| 6 | 11 s | 8 ms | 1.6 ms |
| 50 | 20 s ¹ | 21 ms | 1.3 ms |
| 200 | not run | 67 ms | 1.2 ms |
| 800 | not run | 263 ms | 1.2 ms |
| 1,839 | not run | 539 ms | 1.1 ms |

¹ One-resource template; the 10-resource template exceeded the 300 s limit.

### Whole template, full us-east-1 region (1,839 types)

| Template | union, valid | union, one broken | dispatch, valid | dispatch, one broken |
|---|---:|---:|---:|---:|
| 1 resource | 0.75 s | 0.79 s | <1 ms | <1 ms |
| 10 resources | 5.4 s | 5.3 s | 11 ms | 11 ms |
| 50 resources | 36 s | 39 s | 120 ms | 68 ms |

Loading the region package takes about 2.7 s in both variants, so a full edit-validate cycle for 50 resources is about 3 s with dispatch and about 39 s with the union.

### Loading the region package

| Types in region | old | union and dispatch |
|---:|---:|---:|
| 6 | 4.4 s | 35 ms |
| 50 | 14.8 s | 75 ms |
| 200 | not run | 225 ms |
| 800 | not run | 890 ms |
| 1,839 | not run | 2.7 s |

### Per type, without the region

`TestResourcePerformance` checks a resource against its own `#ResourceTypes` entry. It loads each resource package in 10 to 24 ms and validates one minimal resource in 1 to 3 ms (`AWS::S3::Bucket` is the slowest).

## Findings

- **The intrinsic change made the union usable for small regions but not for full ones.** At equal region size it is 460 to 1,400 times faster than the old generator, which took 4.6 s per resource with only 6 types.
- **The union costs about 0.3 ms per resource type for every resource.** Validation time grows with region size × template size: about 0.5 s per resource in a full region, 36 s for 50 resources. That is too slow for editing.
- **Dispatch removes the dependence on region size.** About 1 ms per resource for 6 or 1,839 types, valid or broken, with errors that name the failing field instead of listing a `Type` conflict for every type.
- **With dispatch, load time is what remains.** Loading 1,839 resource packages takes 2.7 s before any validation. Loading only the packages a template uses (for example per-service region packages, or a generated module for the template's types), or validating in a long-running process that loads once, would remove it.

## Not measured

- Templates with many or large properties per resource. The per-type benchmark gives a floor of 1 to 3 ms per minimal resource.
- The `cue` command line with dispatch. In-process figures exclude process start-up and CLI module loading.
- Repeated runs and variance.
