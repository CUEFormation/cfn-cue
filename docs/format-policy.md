# Format validation policy

Provider schemas carry `format` annotations such as `AWS::EC2::VPC.Id` and
`ipv4-network`. JSON Schema leaves those formats to the validator, so each one
needs a decision. The generator applies the following policy.

## Baseline: compatibility with cfn-lint 1.57.2

By default, a format is enforced exactly as the pinned cfn-lint (1.57.2, commit
`be66fb3e`) checks it. The comparison target is cfn-lint's behavior, not its
documentation, so known cfn-lint quirks are kept. For example, `AWS::EC2::Image.Id`
accepts `[0-9a-z]` rather than hexadecimal digits, and a final newline is
accepted wherever Python's `$` accepts one. The corpus comparison measures
agreement against this baseline.

## Stricter checks

A check stricter than cfn-lint 1.57.2 is enabled only when all of these hold:

1. cfn-lint's own rule or documentation states the intended check, so the
   difference is a cfn-lint defect rather than a policy disagreement.
2. The CloudFormation or service API contract confirms the constraint, so a
   rejected value would also fail at deployment.
3. Properties whose provider schemas deliberately admit other values are
   exempt, and the exemption is scoped to that property.
4. A full-corpus comparison against the previous build shows no new rejection
   that cfn-lint accepts.

Stricter checks still apply only to literal strings. Intrinsic functions and
dynamic references defer them, as they do every other format.

## Decisions

| Format | Decision | Basis |
| --- | --- | --- |
| Identifier formats in `cloudFormationFormatPatterns` | Compatible | cfn-lint's registered patterns |
| `ipv4`, `ipv6`, `ipv6-network` | Compatible | Python's `ipaddress`, as cfn-lint calls it |
| `ipv4-network`, prefix length | Compatible | `ipaddress.IPv4Network`, strict, with host bits checked |
| `ipv4-network`, dotted mask | Compatible | See [IPv4 dotted masks](#ipv4-dotted-masks) |
| `AWS::Logs::LogGroup.Name` | Stricter | See [Log group names](#log-group-names) |
| `AWS::EC2::SecurityGroup.Ids`, `.Names`, `AWS::EC2::Subnet.Ids` | Not enforced | See [List formats](#list-formats) |

### IPv4 dotted masks

Python's `IPv4Network`, which cfn-lint uses for `ipv4-network`, accepts a dotted
netmask or hostmask in place of a prefix length: `10.0.0.0/255.0.0.0` and
`10.0.0.0/0.255.255.255` are both `10.0.0.0/8`. The generated
`fn.#IPv4Network` accepts the same spellings and checks them as Python does:

- The mask must be contiguous: a netmask (ones, then zeros) or a hostmask
  (zeros, then ones). `255.0.255.0` is rejected.
- Mask octets have no leading zeros and are at most 255, so each valid mask has
  one spelling. `fn.#IPv4NetworkMasks` lists all 64 and their prefix lengths.
- A mask that reads both ways is a netmask: `0.0.0.0` is `/0` and
  `255.255.255.255` is `/32`. `10.0.0.1/0.0.0.0` therefore has host bits set.
- The address must be a valid IPv4 address without host bits set for that
  prefix. `10.0.0.1/255.0.0.0` is rejected.

Unsupported forms, which remain differences from Python:

- A prefix length with leading zeros, such as `10.0.0.0/08`. Python reads it as
  `/8`; Go's `netip`, behind CUE's `net.IPCIDR`, rejects it. This is a false
  rejection relative to cfn-lint; no corpus template uses it.
- Non-string values. Like every format, `ipv4-network` constrains strings only;
  Python would also accept an integer or a tuple, which a template cannot
  express as a network string anyway.

### Log group names

`AWS::Logs::LogGroup.Name` is not checked by cfn-lint 1.57.2: rule E1155 builds
the log group pattern but registers it under the `AWS::EC2::Subnet.Id` format
name. Its documented intent is `^[\.\-_\/#A-Za-z0-9]{1,512}\Z`, which matches the
CloudWatch Logs API's log group name (1 to 512 of `.-_/#A-Za-z0-9`). The
generator enforces that pattern. `\Z`, unlike `$`, does not accept a final
newline. An ARN is not a log group name and is rejected.

Exempt properties, whose provider schemas admit other values:

- `AWS::KinesisFirehose::DeliveryStream` `CloudWatchLoggingOptions.LogGroupName`
  also accepts an empty string (minimum length 0 in Firehose's schema and API).
- `AWS::EMRContainers::JobTemplate`
  `ParametricCloudWatchMonitoringConfiguration.LogGroupName` keeps only its
  provider pattern, which allows `${parameter}` placeholders resolved when a
  job starts.

### List formats

`AWS::EC2::SecurityGroup.Ids`, `AWS::EC2::SecurityGroup.Names` and
`AWS::EC2::Subnet.Ids` annotate arrays. They stay unenforced and are reported
in the manifest's conversion diagnostics:

- JSON Schema formats constrain strings only, and cfn-lint 1.57.2 registers no
  checker for them.
- Their items usually carry the matching item format, such as
  `AWS::EC2::SecurityGroup.Id`, which is enforced on each string item.
- Inferring an item rule from the list format would add rejections that
  neither cfn-lint nor the provider schema makes. `AWS::EC2::SpotFleet`'s
  `.Ids` list holds `GroupIdentifier` objects, and EC2 Instance and AutoScaling
  LaunchConfiguration security groups are an `anyOf` of `.Ids` and `.Names`.
