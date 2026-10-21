# Conditional removal with AWS::NoValue

This check runs in `cfn-cue-validate`, after the generated schemas pass.

`{"Ref": "AWS::NoValue"}` removes the property, list entry or `Properties` object that holds it. The generated schemas can't see the template's `Conditions`, so they accept `AssumeRolePolicyDocument: {"Fn::If": ["IsProd", {...}, {"Ref": "AWS::NoValue"}]}` as present. `cfn-cue-validate` adds a check for this case. After the template passes the schemas, it finds every position in each resource's `Properties` that `AWS::NoValue` can remove, directly or through nested `Fn::If`. For each reachable combination of conditions, it checks a copy of the resource with those positions deleted against the resource schema. A required property removed in any reachable combination is reported with the resource, the removed path, and the condition values that select the branch:

```text
Resources.Role: AWS::NoValue removes Properties.AssumeRolePolicyDocument when condition IsProd is false:
Resources.Role.Properties.AssumeRolePolicyDocument: field is required but not present
```

The same copies catch required fields in nested objects (`Properties.Policies[0].PolicyDocument`), a removed `Properties` object for a type with required properties, and at-least-one groups whose members are all removed. Only errors that name a removed position as a required field are reported. Optional properties and list entries can be removed freely. If the template already fails the schemas, that error is reported instead.

Reachability uses the template's `Conditions` and `Parameters` (`internal/conditions`). The semantic pass has its own condition model; [conditions.md](conditions.md) compares the two:

- `Fn::And`, `Fn::Or`, `Fn::Not` and `Condition` are evaluated as written. A branch is reachable only if some assignment satisfies every enclosing `Fn::If` choice and the resource's `Condition` attribute. A resource whose `Condition` can never hold is never created, so its branches aren't checked.
- `Fn::Equals` of two literals is a known boolean, compared by string form (`"1"` equals `1`). An expression compared with literals takes at most one of them, so `Env = prod` and `Env = dev` can't both hold. A `Ref` to a `String` or `Number` parameter with `AllowedValues` takes only those values.
- Every other comparison, such as two different `Ref`s or a `Fn::FindInMap` result, is an unknown boolean, independent of the rest.
- An undeclared, cyclic or malformed condition is unknown, not true or false. Both of its branches stay reachable. Each use of the name has the same value, so a resource with `Condition: X` can't reach `X`'s false branch. Undeclared names themselves are left to cfn-lint.

Compared with cfn-lint 1.57.2 on the IAM Role cases in `internal/validate/novalue_test.go`, both report one branch, either branch, both branches, nested conditionals, and the resource's own `Condition` the same way. The model is stricter about what is unreachable than cfn-lint in three cases, so it accepts templates that cfn-lint rejects:

- a constant condition (`Fn::Equals: [a, a]`; cfn-lint warns W8003 and checks both branches);
- different literal comparisons with a pseudo parameter, such as two `AWS::Region` values;
- a resource whose `Condition` is constantly false.

It also reports a `Properties` object that `Fn::If` replaces with `AWS::NoValue` when the type has required properties. cfn-lint accepts that, but CloudFormation can't create the resource without them.

Remaining approximations:

- Values are not resolved, so comparisons involving `Fn::FindInMap`, `Fn::Select`, `Fn::Join` and similar functions don't constrain each other. They may make an unreachable branch look reachable.
- A query over more than 16,384 assignments of the underlying comparisons is undecided. If a resource has more than 32 distinct removal sets, or its combinations are undecided, each removal is checked with the others the same choices force. That fallback misses failures that need independent removals together, and an undecided removal is not reported.
- Other rules are not checked against removals. A list that `AWS::NoValue` leaves shorter than `minItems`, or a cross-property rule that a removal breaks, is not reported. The copies keep every other `Fn::If` as a present value, which may not match any single condition assignment. In the corpus, the widdix DynamoDB template removes a `KeySchema` sort key while keeping conditional local indexes; checking that rule would reject a template cfn-lint accepts.
- `AWS::NoValue` inside other intrinsic arguments, `Fn::ForEach` output, and resource attributes other than `Properties` are not checked. Branches are not otherwise specialized: a kept `Fn::If` value is still checked only for its result type.
- The check runs in `cfn-cue-validate` and `internal/validate`. Importing `regions/<region>:cf` and unifying a template with `#Template` in CUE does not include it.
