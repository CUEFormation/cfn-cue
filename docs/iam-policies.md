# IAM policies

IAM policy properties are checked against a profile for each policy kind, not just the provider schema.

`iam/iam.cue` defines the IAM policy grammar and one profile per policy kind. In `resources/` schemas, the generator replaces the provider type of these properties with a profile:

| Profile | Properties |
|---|---|
| `#TrustPolicyValue` | `AWS::IAM::Role.AssumeRolePolicyDocument` |
| `#IdentityPolicyValue` | inline `Policies[].PolicyDocument` on IAM Role, User and Group; `PolicyDocument` on IAM Policy, ManagedPolicy, RolePolicy, UserPolicy and GroupPolicy; `AWS::SSO::PermissionSet.InlinePolicy` |
| `#S3BucketPolicyValue` | `AWS::S3::BucketPolicy.PolicyDocument` |
| `#KMSKeyPolicyValue` | `KeyPolicy` on `AWS::KMS::Key` and `AWS::KMS::ReplicaKey` |
| `#ResourcePolicyValue` | `AWS::SNS::TopicPolicy.PolicyDocument`, `AWS::SQS::QueuePolicy.PolicyDocument`, `AWS::OpenSearchService::Domain.AccessPolicies` |
| `#ECRRepositoryPolicyValue` | `AWS::ECR::Repository.RepositoryPolicyText` |
| `#ServiceControlPolicyValue`, `#ResourceControlPolicyValue` | `AWS::Organizations::Policy.Content` when `Type` is `SERVICE_CONTROL_POLICY` or `RESOURCE_CONTROL_POLICY` |

The profiles check which elements each policy kind allows. Trust policies need a principal and take no `Resource`. Resource policies need a principal and a resource, except ECR repository policies, where `Resource` is optional. Identity policies take no `Principal`. SCP `Allow` statements may only name `Resource: "*"`. Customer-managed RCPs only deny, with `Principal: "*"`. They also check `Version`, the `Action` and ARN shapes, condition operators, and the exactly-one rules for `Action`/`NotAction` and the other element pairs. Every value also accepts an intrinsic, which is not evaluated: an intrinsic `Effect` skips the SCP `Resource` rule, for example. A list element may also be a dynamic reference. `Statement`, a single statement, `Principal` or `Condition` can be `Fn::If`, and a statement, `Condition` or the whole policy can be removed with `AWS::NoValue`. A whole-policy `Fn::If` may nest another one in either branch. Condition keys may use OIDC issuer namespaces such as `token.actions.githubusercontent.com:sub`. The profiles don't check whether an action, resource or condition key exists; leave that to IAM Access Analyzer or cfn-lint.

A placement that no longer matches the provider schema is reported as an `iamPolicy` diagnostic. `literal/` schemas keep provider types. Known limits with CUE v0.18.0-rc.1:

- A policy written as a literal JSON string is checked with `json.Validate` against the same profile, but its errors don't give the path of the failing field.
- Repeated statement `Sid`s are not checked here; the semantic pass reports them (see [semantic validation](semantic-validation.md#iam-statement-ids)).
- A failing policy often produces a vague error such as `Statement: field not allowed`. To find the failing field, unify the statement with its statement profile, for example `iam.#TrustStatement & {...}`.

Other templates can import `iam` and unify profiles with their own values, or add organization rules on top.
