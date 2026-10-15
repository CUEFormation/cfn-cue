# Provider patterns with lookarounds

JSON Schema patterns are ECMAScript regular expressions, applied as an
unanchored search. CUE uses RE2, which has no lookaheads or lookbehinds. The
generator rewrites a provider pattern with lookarounds into RE2 patterns that
accept exactly the same strings, or keeps reporting it in the manifest. It does
not drop part of a pattern and enforce the rest.

The code is in `internal/generate/lookaround.go`.

## Translation

When a match must start at the beginning of the string, a lookahead that
follows a fixed-width prefix is checked at a known offset. A positive
lookahead becomes another pattern the string must match, and a negative one
becomes a pattern it must not match:

| Source | Must match | Must not match |
|---|---|---|
| `^(?!aws:)[a-zA-Z+-=._:/]+$` | `^[a-zA-Z+-=._:/]+$` | `^(?:aws:)` |
| `^[a-zA-Z_](?!.*--)[a-zA-Z0-9_-]*$` | `^[a-zA-Z_][a-zA-Z0-9_-]*$` | `^(?s:.){1}(?:.*--)` |
| `^(?=^[a-z0-9-]+$).{3,64}$` | `^.{3,64}$`, `^(?:^[a-z0-9-]+$)` | |
| `^[0-9a-z\.\-]*(?<!\.)$` | `^[0-9a-z\.\-]*$` | `(?:\.)$` |

The third row is Redshift Serverless's `WorkgroupName`. In CUE, the first row
becomes `=~"^[a-zA-Z+-=._:/]+$" & matchN(0, [=~"^(?:aws:)"])`.

Other supported forms:

- A lookbehind directly before `$` is checked at the end of the string. One at
  a fixed offset is checked there when its body has a fixed width.
- `(?![\s\S])` is `$`.
- A positive lookahead whose body starts with `^` anchors the match to the
  start of the string.
- Without an anchor, a negative lookahead whose body starts with `^`, as in
  `(?!^[0-9]+$)([a-zA-Z0-9-_' ']+)`, only applies to a match at offset 0. A
  match found later always passes it, so ECMAScript accepts `"12"` (matching
  `"2"`) and rejects `"1"`. The translation is a disjunction of the two cases.
- Top-level alternatives are translated separately. Alternatives inside an
  unrepeated group are distributed, so `^(?:(?=A)|(?=B))R$` becomes two
  alternatives.
- Negated patterns apply only to strings, as `pattern` does, so numbers and
  booleans are checked through their string form as before.

An object whose only key pattern has lookarounds that exclude names, such as
tag maps with `^(?!aws:)[a-zA-Z+-=._:/]+$` and `additionalProperties: false`,
gets the wider key pattern for the values and a key pattern for each excluded
form whose value must be absent. Other key patterns with lookarounds still open
the object, with a diagnostic.

The pieces keep the RE2 interpretation the generator already applies to every
provider pattern: `.` excludes only `\n`, `\s` is ASCII whitespace, and an
inline `(?i)` applies to the rest of its group, as in Java and RE2.

## Not translated

Each of these keeps its `pattern is incompatible with RE2` diagnostic, with
the reason appended after `lookarounds not translated:`. `-strict` refuses to
publish output that has them.

| Reason | Example | Patterns |
|---|---|---|
| lookaround inside a repeated group | `^(((?!-)[A-Za-z0-9-]{0,62}[A-Za-z0-9])\.)+…` (Amplify `DomainName`) | 9 |
| lookahead at a variable position | `(?=.{1,32}$)[a-zA-Z0-9](-*[a-zA-Z0-9])*` (Cloud Map `HttpNamespace.Name`), RUM `Domain` | 6 |
| quantified assertion | `^(\/|(\/(?!\.)+[^…]+){1,4})$` (EFS access point path) | 2 |
| repeat count over RE2's 1,000 | `^(?=.{20,2048}$)arn:aws…:secretsmanager:…` | 3 |
| lookbehind at a variable position | `…([a-zA-Z0-9-]){0,62})(?<!-)(\/[0-9]{1,9})?$` (SageMaker model package) | 1 |
| backreference | Security Hub's ISO 8601 date pattern | 1 |
| key pattern with a positive lookahead | EventBridge `Subscriber` header parameter names | 1 key |

In the pinned snapshot this leaves 31 diagnostics, across 22 unique patterns
and one key pattern. Translating patterns removed 341, of 1,092 in total.

## Service-rule conditions

cfn-lint extension rules are separate. A condition pattern RE2 can't compile
still skips the whole rule, because the rule's effect depends on cfn-lint's own
evaluation of the condition. The existing `^(?!X).*$` rewrite for the RDS rules
is unchanged. No vendored rule has another lookaround.

## Evidence

`internal/generate/testdata/lookaround/patterns.json` lists every translated
snapshot pattern, four synthetic ones and a shared set of 221 sample strings.
Some patterns add their own examples. `scripts/lookaround-oracle.mjs` evaluates
each pattern with Node's `RegExp`, using the `u` flag that JSON Schema 2020-12
specifies, and writes `oracle.json`.

Three forms that are not ECMAScript are rewritten before the oracle runs:
`\A`, a mid-pattern `(?i)` and a `-` after a class escape such as `\p{L}-_`.
`oracle.json` records each rewritten pattern. Samples avoid characters where
RE2 and ECMAScript disagree for any pattern (CR, `\v`, U+2028 and Unicode
spaces).

```sh
node scripts/lookaround-oracle.mjs   # after changing patterns.json
go test ./internal/generate -run Lookaround
```

The tests check that:

- every translation agrees with ECMAScript on every sample;
- each pattern accepts some sample, and rejects some sample only because of its
  lookarounds. Five are exempt: the rest of the pattern already excludes the
  same strings, as `patterns.json` notes for each;
- every pattern with lookarounds in the pinned snapshot is in the oracle or in
  the unsupported list, with its reason;
- generated CUE enforces the translations, including tag maps, intrinsics,
  dynamic references and numbers checked through their string form.

Where a property has both a cfn-lint format and a translated provider pattern,
as several S3 bucket name properties do, both apply, as in cfn-lint. The
provider pattern sometimes repeats the format, for example "doesn't end with
a dot". It also adds constraints the format lacks, such as rejecting IP-address
names.

## Corpus check

The downloaded-example survey was rerun on the 548 templates that use a
resource type whose schema changed, before and after this change, with a
30-second deadline. One outcome changed: troposphere's `RedshiftServerless`
example, whose `WorkgroupName` `RedshiftServerlessWorkgroup` fails
`^(?=^[a-z0-9-]+$).{3,64}$`, moved from accepted by CUE and rejected by
cfn-lint to rejected by both. No template became a CUE-only rejection.
