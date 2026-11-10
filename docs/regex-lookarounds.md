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

### Rewrites for other positions

When an alternative has a lookaround the steps above can't place, three
rewrites are tried, in `internal/generate/lookaround_rewrite.go`. Each one
replaces part of the pattern with an equivalent part, so the result is still
exact. Alternatives the positional translation already handles are never
rewritten, so their output doesn't change.

- **One-character lookarounds.** A lookaround whose body only tests the
  character next to it, such as `(?!-)`, `(?![:*$])`, `(?!\d+)` (which matches
  wherever `\d` does) or `(?<!-)`, is folded into the atom that consumes that
  character. The atom's class is narrowed, or widened for a positive
  lookaround. This works anywhere, including inside a repeated group, because
  the body only reads a character the atom consumes. When the atom can be
  skipped (`{0,n}`, `*`, `?`), the rewrite has two alternatives: one where the
  atom takes the character and one where the next atom does. A lookahead with
  no atom after it in its group, or a lookbehind with none before it, can't
  be folded.

  | Source | RE2 |
  |---|---|
  | `((?!-)[A-Za-z0-9-]{0,62}[A-Za-z0-9])\.` | `((?:[0-9A-Za-z][A-Za-z0-9-]{0,61}[A-Za-z0-9]\|[0-9A-Za-z]))\.` |
  | `((?!-)[A-Za-z0-9-]{1,63}(?<!-)\.)+` | `((?:[0-9A-Za-z][A-Za-z0-9-]{0,61}[0-9A-Za-z]\|[0-9A-Za-z])\.)+` |
  | `^((?![:*$])[\x00-\x7F]){1,255}` | `^([\x{0}-\#\%-\)\+-9\;-\x{7f}]){1,255}` |
  | `(:((?!\d+)[0-9a-zA-Z-_]+))?$` | `(:([\-A-Z\_a-z][0-9a-zA-Z-_]*))?$` |

- **Optional atoms before a lookahead.** Optional atoms and unrepeated groups
  before the last lookaround are distributed until each alternative has the
  lookaround at a fixed offset: `^(-?)P(?=\d|T\d)…$` becomes `^P(?=\d|T\d)…$`
  and `^-P(?=\d|T\d)…$` (IAM Identity Center's `SessionDuration`).
- **A negative lookahead before every character.** In `^(?:(?!B)C)*$`, or
  with `+` or `{m,n}`, where `C` matches one character, every position of the
  string starts an iteration, so `B` must not match anywhere. That is
  `=~"^(?:C)*$" & !~"B"`, provided `B` consumes at least one character
  (Well-Architected `AgentGoal` uses `^(?:(?!\$\{[a-zA-Z]+:)[\P{C}])+$`).

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

| Reason | Patterns (diagnostics) | Examples | Why not translated |
|---|---|---|---|
| lookahead at a variable position | 5 (7) | `^(arn:.+:iam::(…\|(?=.*\*)[0-9*?]{1,12}\|…):role/.+\|\*)$` (CloudFormation `GuardHook` and `LambdaHook` `StackRole`), RUM `Domain` and `DomainList`, `(?!\s*$).+` (SageMaker `Cluster` alarm names), `(?=.{1,32}$)[a-zA-Z0-9](-*[a-zA-Z0-9])*` (SageMaker `Project` template names), Pipes EventBridge `Source` | The lookahead follows a variable-width part such as `.+`, or the pattern is unanchored, so the lookahead and the rest must hold at the same unknown position. That is an intersection that a conjunction of RE2 patterns can't express. The few that could be solved case by case would need reasoning specific to each pattern. |
| lookaround inside a repeated group | 2 (2) | `^arn:(?=…)((\|(?=[a-z0-9-.]{1,63})(?!\d{1,3}(\.\d{1,3}){3})(?![^:]*-{2})…):){4}…` (FSx `Backup` and `FileCache` `ResourceARN`) | Multi-character lookaheads at a different offset in each of four repetitions. |
| quantified assertion | 2 (2) | `^(\/\|(\/(?!\.)+[^…]+){1,4})$` (EFS and S3 Files access point paths) | `(?!\.)+` is a syntax error with the `u` flag JSON Schema specifies. Python and ECMAScript without `u` read it as `(?!\.)`, so the source has no single meaning to preserve. |
| repeat count over RE2's 1,000 | 3 (3) | `^(?=.{20,2048}$)arn:aws…:secretsmanager:…`, `(?=.{1,1024}$)(https)://…` (SageMaker `Project` template URLs), `^(?!arn:)[!-~]{1,1024}$` (Cloud Map `HttpNamespace` names) | The repeat count, not the lookaround, is what RE2 rejects. It's the same limit behind 152 diagnostics on patterns without lookarounds, and it needs a separate fix. |
| backreference | 1 (1) | Security Hub's ISO 8601 date pattern | RE2 has no backreferences. |
| key pattern with a positive lookahead | 1 key (1) | EventBridge `Subscriber` header parameter names | A key pattern can only exclude names (see above). The pattern itself is translated. |

In the pinned snapshot this leaves 16 diagnostics, across 13 unique patterns
and one key pattern. The rewrites removed 15 more diagnostics, leaving 736 in
the manifest.

## Service-rule conditions

cfn-lint extension rules are separate. A condition pattern RE2 can't compile
still skips the whole rule, because the rule's effect depends on cfn-lint's own
evaluation of the condition. The existing `^(?!X).*$` rewrite for the RDS rules
is unchanged, and the rewrites above aren't applied to conditions either. No
vendored rule has another lookaround.

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
  lookarounds: the sample matches the wider pattern, or the source with its
  lookarounds deleted. Five are exempt: the rest of the pattern already
  excludes the same strings, as `patterns.json` notes for each;
- every pattern with lookarounds in the pinned snapshot is in the oracle or in
  the unsupported list, with its reason;
- generated CUE enforces the translations, including tag maps, intrinsics,
  dynamic references and numbers checked through their string form.

Where a property has both a cfn-lint format and a translated provider pattern,
as several S3 bucket name properties do, both apply, as in cfn-lint. The
provider pattern sometimes repeats the format, for example "doesn't end with
a dot". It also adds constraints the format lacks, such as rejecting IP-address
names.

The patterns translated by the rewrites were also checked against Python's
`re` and `regex` modules, which cfn-lint uses, on 20,000 random and mutated
strings each, with no disagreement. The strings avoided `\n`, where Python's
`$` differs, and non-ASCII digits, which Python's `\d` matches.

## Corpus check

The rewrites were checked on the 37 downloaded templates that use one of the
nine affected resource types, before and after, with a 30-second deadline.
No outcome changed, and CUE's total time was the same.

The first translation was checked as follows.

The downloaded-example survey was rerun on the 548 templates that use a
resource type whose schema changed, before and after this change, with a
30-second deadline. One outcome changed: troposphere's `RedshiftServerless`
example, whose `WorkgroupName` `RedshiftServerlessWorkgroup` fails
`^(?=^[a-z0-9-]+$).{3,64}$`, moved from accepted by CUE and rejected by
cfn-lint to rejected by both. No template became a CUE-only rejection.
