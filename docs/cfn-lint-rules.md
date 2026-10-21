# Service rules from cfn-lint

Some of cfn-lint's per-resource service rules are compiled into the generated schemas.

cfn-lint adds service rules to the provider schemas as JSON Schema "extension" files. The generator applies 49 of them, copied unchanged from cfn-lint v1.57.2 into `internal/generate/cfnlint/extensions/`, to 34 resource types. Examples: an S3 bucket with a public `AccessControl` needs `OwnershipControls`; a Lambda target group can't set a port; `ZipFile` code needs a Node.js or Python runtime; an Aurora instance can't set `AllocatedStorage`; a Fargate task needs a supported `Cpu` and `Memory` combination. `internal/generate/extensions.go` lists each rule ID, file and property path.

Only error-level rules that check one resource are used. Rules that look at other resources, list instance types per region, or carry cfn-lint's multi-megabyte RDS instance class tables remain with cfn-lint, as do warnings.

Most rules become CUE validators that re-check the whole resource, so their cost grows with the resource. Seven rules with several conditions on large resources are instead compiled to comprehensions that read only the fields they name (`internal/generate/scoped_rules.go`): the ECS task definition rules (E3042, E3047, E3048), the DynamoDB billing mode rules (E3638, E3639) and the RDS engine version rules (E3690, E3691). A failure names the rule, for example `E3638: when BillingMode is PAY_PER_REQUEST, ProvisionedThroughput capacity units must be 0`; a property a rule requires reports `field is required but not present`. As validators, these seven added up to 457 ms to a corpus template and 34% to the total CUE time of the 101 templates that use them. Compiled this way, they add a median of 1 ms, at most 10 ms, and 3% in total. [corpus/reports/2026-10-14-scoped-rules](../corpus/reports/2026-10-14-scoped-rules/README.md) has the measurements.

The generator rewrites the files to approximate cfn-lint’s behavior, with the following compatibility rules and limits:

- `const` and `enum` compare loosely, as in cfn-lint: `"0"` equals `0`, and `"true"` equals `true`.
- A numeric condition such as `maximum: 60` is not met by a string. cfn-lint compares `"45"` as a number, so CUE skips some rules it would apply, but never applies one to a numeric string cfn-lint exempts.
- An intrinsic never satisfies a rule's condition, so `AccessControl: {Ref: Acl}` doesn't require `OwnershipControls`. cfn-lint instead resolves the `Ref` to the parameter's values, so it can report a rule CUE skips. In the seven compiled rules this also holds under `not`: `BillingMode: {Ref: Mode}` doesn't require `ProvisionedThroughput`.
- Outside conditions, the seven compiled rules accept an intrinsic or dynamic reference wherever they constrain a value, and compare a numeric string as a number, as cfn-lint does: `ReadCapacityUnits: "0"` fails E3639. cfn-lint reports E3691 for `EngineVersion: "{{resolve:ssm:/name}}"`; CUE accepts it.
- cfn-lint compares Python's string form, so the YAML float `EngineVersion: 8.0` matches `"8.0"` and the integer `8` doesn't. The compiled rules match this; validator rules match neither.
- `^(?!X).*$` lookahead patterns become a negated prefix, and the RDS rules that lowercase `Engine` match it case-insensitively. A rule with any other pattern RE2 can't compile is skipped with a diagnostic, because dropping a pattern from a condition would apply the rule too widely. Provider-pattern lookaround translation is not used for conditions.
- `not` over required properties becomes `false` property schemas, and `anyOf` in a condition becomes a double `not`. CUE treats a missing required field in a disjunction as incomplete rather than failing, which would otherwise make these rules reject every resource of the type or always apply.

A rule whose path ends at a referenced definition is added to that definition, so it applies wherever the definition is used, while cfn-lint applies it only at the listed path. Conditions in `Fn::If` values aren't evaluated. These rules are approximations of cfn-lint's, not AWS documentation; disable them with `-cfn-lint-rules=false`.
