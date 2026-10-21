# Two regional schema representations

Every generator run emits both versions for every selected region. The existing
full-resource import remains `regions/<region>:cf`; the lightweight import is
`regions/<region>/types:types`. They are separate CUE packages: importing the
type-name package does not load the full resource registry. To use one resource
type without loading either, import its region-independent per-type package,
such as `AWS/S3/Bucket`; see [importing resource types](schemas.md#importing-resource-types).

| Definition | Full resource package | Type-name package |
| --- | --- | --- |
| `#Template` | Template sections, regional availability, resource properties and intrinsics | Template sections and regional availability |
| `#Resource` | Absent; use `#ResourceTypes[Type]` | Common resource attributes with a regional `Type` name |
| `#Resources` | Logical ID map; each resource is checked against `#ResourceTypes[Type]` | Logical ID map with resource envelopes |
| `#ResourceTypes` | Map from type names to full resource definitions | Absent |
| `#ResourceType` | Absent | Disjunction of available resource type names |

The lightweight version deliberately leaves `Properties` unconstrained, including
property presence, primitive types, required fields, allowed keys and intrinsics
inside properties. Intrinsics in common template sections, such as `Conditions`
and `Outputs`, retain their generated validation. LanguageExtensions fragments
and SAM type names follow the same generator options as the full version.
`Custom::...` names are allowed when the regional CustomResource provider is
available. Availability describes the selected generated catalog; `-resources`
also restricts the type-name version.

For example, an authored template can select the lightweight regional definition:

```cue
package example

import region "github.com/cueformation/cfn-cue/regions/us-east-1/types:types"

template: region.#Template & {
    Resources: Bucket: {
        Type: "AWS::S3::Bucket"
        Properties: BucketName: {"Fn::Sub": "${Name}-bucket"}
    }
}
```

For direct YAML validation, run from the generated module root with a compatible
CUE CLI, currently v0.18.0-rc.1:

```sh
# Type names and common template structure.
cue vet -c ./regions/us-east-1/types -d '#Template' /path/to/template.long.yaml

# Complete property and intrinsic validation; this can be expensive at full scale.
cue vet -c ./regions/us-east-1 -d '#Template' /path/to/template.long.yaml
```

CloudFormation short tags such as `!Ref` and `!Sub` must first be expanded into
ordinary YAML mappings. The corpus normalizer already produces long-form YAML.
The direct Go validator continues importing only schemas used by the template;
it does not switch to the less restrictive type-name schema.

## Reproduce performance measurements

The benchmark requires Python 3 (standard library only), Go 1.26+, and a compatible
CUE CLI. It uses the checked-in schema snapshot, without fetching examples. Use
an empty output directory for each run so previous measurements are preserved.

```sh
./scripts/regions.sh benchmark --cue /path/to/cue \
  --sizes 2,10,50,all --timeout 20 --repeats 3 \
  --out .region-performance-alpha3
```

Default samples begin with S3 Bucket and IAM Role, then use a deterministic hash
order of the remaining types. Samples are nested. Both representations use the
same resource set, and the full scale uses the checked-in generated module.
Filtered modules are generated once outside the timed commands; their source
checksums and regional resource bindings must match the checked-in module.
Generator flags for LanguageExtensions and SAM are retained.

Each observation runs `cue eval ... -e '#Template'` in a fresh process. This
measures loading, evaluation and formatting together, not separate evaluator
phases. Optional template checks also run `cue vet -c ... -d '#Template'` in
fresh processes:

```sh
./scripts/regions.sh benchmark --cue /path/to/cue \
  --sizes 2,all --timeout 20 \
  --template /path/to/template.long.yaml \
  --out .region-performance-with-template
```

Template-used types are selected first, and every requested size must accommodate
them. The input may be normalized YAML or JSON. A template that uses unknown
types or unexpanded resource loops cannot be used to select a valid scale sample.

Reports preserve every repeat, selected type lists, schema/source and executable
fingerprints, CUE and embedded Go versions, platform, template checksum, deadline,
memory target, CPU time, exit status, and command output. Peak RSS is measured per
CLI process on Linux/macOS, including timed-out processes; it is unavailable on
other platforms. `GOMEMLIMIT` is a soft Go target, not a hard memory cap. The
default is 2GiB. Operating-system file caches are not cleared.

Read `report.md` for the table and `report.json` for comparisons. A timeout is an
incomplete observation, not a completed duration. Timeouts do not fail the harness;
abstract schema errors do. Template rejection is recorded without treating the
two schema representations as equivalent validators. Compare runs only after
checking their resource sets, template, source, schema fingerprints, platform,
budgets and memory target; when changing CUE, its executable fingerprint is
expected to change.

The **Regional CUE performance** GitHub workflow can be dispatched manually with
a CUE version, scale list, deadline and repeat count. It preserves reports as run
artifacts. Both GitHub workflows use `ubuntu-26.04` to avoid changing the OS family
implicitly when `ubuntu-latest` migrates. GitHub still updates that image's
software and kernel, so compare the recorded platform details across runs.
The performance workflow becomes available in the Actions UI once it reaches the
default branch. Normal CI checks both representations and the timeout harness
without running a potentially expensive full-region benchmark on every commit.
