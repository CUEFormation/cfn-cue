# Condition models

Two packages decide CloudFormation conditions. They landed in parallel:

- `internal/conditions` (issue #11, PR #24) decides which branches of `Fn::If`
  can select `AWS::NoValue`. `internal/validate/novalue.go` uses it to build
  the removal variants of each resource.
- `semantic.Conditions` in `internal/semantic/conditions.go` (issue #10, PR #20)
  classifies the guards of a reference for the `conditional-target` and
  `dependency-cycle` rules.

Both make the same core choices, but they answer different questions for
different callers and differ in several edge cases. **They are kept as two
packages on purpose.** This note records where they agree, where they differ,
and what each caller needs, so a change to one can be checked against the
other.

## What the two agree on

Both models compile `Conditions` into propositional formulas and enumerate the
underlying comparisons:

- `Fn::And`, `Fn::Or`, `Fn::Not` and `Condition` are followed as written.
- `Fn::Equals` of two scalar literals is decided by string form, so `"1"`
  equals `1` and `true` equals `"true"`.
- An expression compared with literals takes at most one of them. `Env ==
  "prod"` and `Env == "dev"` exclude each other.
- A `Ref` to a parameter with `AllowedValues` takes one of those values.
  Comparing it with a value outside the list is false.
- Two identical expressions are equal. Any other comparison, such as two
  different `Ref`s, is an independent unknown.
- An undeclared condition, or an expression of unrecognized shape, is an
  unknown. Each use of the same name or expression has the same value.

Both read the template only. Neither substitutes a parameter default or the
validation region for `AWS::Region`.

## Where they differ

Each row was checked by running both models on the same template and query
on `e3648424`. "Semantic" is `semantic.Analyze(t).Conditions.Classify`.
"Conditions" is `conditions.New(t).Satisfiable` and `Combinations`.

| # | Input | `semantic.Conditions` | `internal/conditions` | Accepted by CloudFormation |
| --- | --- | --- | --- | --- |
| 1 | `AllowedValues` on an AWS-specific parameter type, such as `AWS::EC2::KeyPair::KeyName` or `AWS::SSM::Parameter::Value<String>` | Restricts the `Ref`. Only `CommaDelimitedList` and `List<…>` types are excluded, so `AWS::SSM::Parameter::Value<List<String>>` is also restricted | Restricts only `String` and `Number` | Yes |
| 2 | `AllowedValues` with a non-scalar entry | Keeps the scalar entries as the domain. With none left, the domain is empty and every query on the parameter is `Unknown` | Ignores the list | No; the schema rejects it |
| 3 | `Fn::And` or `Fn::Or` with 1 or more than 10 arguments | Evaluates the arguments | Unknown | No; the schema rejects it |
| 4 | Cyclic conditions, such as `A: And[Condition B, Equals[Ref P, x]]`, `B: Condition A` | Compiles every condition up front in name order and breaks the cycle at the first back reference: `B = cycle(A)`, `A = cycle(A) ∧ P=x`. `B ∧ ¬A` is `Sometimes` | Compiles on demand. The first condition a query touches is where the cycle breaks, and the result is cached for later resources: `A = u(B) ∧ P=x`, `B = A`. `B ∧ ¬A` is unsatisfiable | No |
| 5 | Query size limit | At most 16 atoms per query, counted after constant folding. An expression compared with *k* literals is *k* atoms | At most 2¹⁴ assignments per query, without folding: the product over expressions of their choices (*k* + 1 values, or every allowed value) times 2 per unknown | Not applicable |
| 6 | Number spelling, such as `Equals["1", 1.0]` or `AllowedValues: [1.0]` | `"1"`: `internal/validate` decodes the template into `float64` | `"1.0"`: `removalVariants` decodes again with `UseNumber` | Yes |

Row 6 is a difference between callers, not between models. Both models
spell `float64` with `json.Marshal` and `json.Number` as written. They give
different answers because `validate.Check` hands them differently decoded
templates.

The two also differ in API:

| | `semantic.Conditions` | `internal/conditions` |
| --- | --- | --- |
| Construction | Unexported `newConditions`, called by `semantic.Analyze` | `conditions.New(template)` |
| Query | `Classify([]Guard) Truth` (`Always`, `Never`, `Sometimes`, `Unknown`), and unexported formula composition for the cycle rule | `Combinations(names, given) ([][]bool, bool)` and `Satisfiable([]Literal) (bool, bool)` |
| Literal type | `Guard{Condition, Value}` with JSON tags; it appears in `semantic.Diagnostic.Conditions` | `Literal{Condition, Value}` with `String()` |
| Name lookup | `Defined` | `Declared` |
| Cycles | Records the cyclic names for the `condition-cycle` rule | Not recorded |

## What each caller needs

**Removal variants** (`internal/validate/novalue.go`) collect, per resource,
the `Fn::If` choices on the path to each `AWS::NoValue`. They then need:

- `Combinations(names, given)`: every reachable assignment of the conditions
  those choices name, given the resource's own `Condition`. The variants and
  their messages are built from these rows.
- `Satisfiable(literals)` for the fallback, which checks each removal on its
  own when there are more than 32 removal sets or `Combinations` is
  undecided.
- The 2¹⁴ limit as it is today. It is reached in the corpus (below).
- Numbers decoded with `UseNumber`, as the variants are re-encoded and
  checked by CUE.

**The semantic pass** (`internal/semantic`) records guards on every reference.
It needs:

- A four-valued classification. `conditional-target` is an error for
  `Always`, a warning for `Sometimes` and `Unknown`, and nothing for `Never`.
- Classification of formulas built from guards, not just conjunctions. A
  dependency edge is the disjunction of its references' guard sets, and a
  cycle is the conjunction of its edges.
- The list of cyclic conditions, which requires compiling every condition.
- `Guard` as a JSON type, since it is part of the validator's output.

Both needs reduce to one primitive. A formula over named conditions is
`Always` when it is true in every reachable row of `Combinations` over the
names it mentions, `Never` when it is false in every row, and `Sometimes`
otherwise. The edge and cycle formulas only combine named conditions, so they
can be evaluated row by row.

## How much the differences matter

The differences were measured over every normalized template in
`.corpus/normalized` (1,092 templates; 213 declare conditions) and the
checked-in `corpus/smoke` and `corpus/semantic` cases:

- **Rows 1–3 and 6** don't change any corpus result. No template sets
  `AllowedValues` on an AWS-specific type. One template (`78e4f2506c0f2242`)
  has a non-scalar `AllowedValues` entry, but it declares no conditions. One
  template (`f15ff9bf7b4920a8`, a cfn-lint "bad" fixture) has `Fn::And` with
  1 and 11 arguments, but it has no `Resources` and stops in the input phase.
  One template compares a non-integer number (`1.234`), which both spellings
  agree on.
- **Row 4**: the same fixture is the only one with cyclic conditions, so no
  change.
- **Row 5 is binding.** Removal-variant queries reach exactly 2¹⁴
  assignments in three templates (`991fe4669e36b637`, `4ffc49ecc6f12f61`,
  `10d7a9b22b158286`). One query in `478f2237d9d3539f`, the widdix DynamoDB
  table that the README describes, exceeds the limit and uses the fallback.
  Measuring `Combinations` any other way would change those results. The
  semantic pass peaks at 6 atoms (`35af08ef0f69b870`), so any limit of at
  least 2⁶ assignments decides every semantic query in the corpus.

Existing tests:

- `conditions.TestUndecided` pins the 2¹⁴ limit: 15 independent comparisons
  are undecided. Under the semantic rule of 16 atoms, they would be decided.
- No other test in either package depends on rows 1–6. The self-cycle
  `Loop` in `TestCombinations` and the two-condition cycle in
  `semantic.TestReferences` give the same answer both ways, and the malformed
  case in `TestCombinations` is `Fn::Equals` with one argument, which both
  treat as unknown.
- `internal/validate` tests (`TestNoValueRequiredProperties`, the IAM Role and
  corpus tests) and `semantic.TestClassify` use only behaviour from
  [What the two agree on](#what-the-two-agree-on).

## Decision

The models stay separate. Merging them would change results: each row in
[Where they differ](#where-they-differ) has a small template on which one
caller's answer would change, so a merge would mean six behaviour decisions,
not a refactor. The two callers also need different things. Removal variants
need the rows of `Combinations` and the 2¹⁴ limit that the corpus reaches.
The semantic pass needs a four-valued classification of guard formulas and
the list of cyclic conditions. Today no corpus template is affected by any
difference, so nothing is gained by forcing one answer.

When changing either model:

- Keep the choices in [What the two agree on](#what-the-two-agree-on) in
  step. A change to one of them should be made in both packages, or added to
  [Where they differ](#where-they-differ) with the reason.
- A change to a behaviour in the differences table needs no change in the
  other model. Update the table.
- Rows 4 and 6 can be fixed on their own without a merge.
  `internal/conditions` could compile every condition up front, so a cyclic
  template's answers don't depend on which resource asks first.
  `validate.Check` could decode the template once with `UseNumber` and give
  that copy to both checks.

If a merge is reconsidered, `internal/conditions` is the better base. Its
finite-domain variables make exclusivity and `AllowedValues` part of the
domain, and `Classify` can be built from `Combinations`, but not the other way
round. The merge would first have to settle each row of the differences table
and keep the 2¹⁴ `Combinations` limit exactly as it is counted today.
