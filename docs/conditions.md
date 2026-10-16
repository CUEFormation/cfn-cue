# Condition models

Two packages decide CloudFormation conditions. They landed in parallel:

- `internal/conditions` (issue #11, PR #24) decides which branches of `Fn::If`
  can select `AWS::NoValue`. `internal/validate/novalue.go` uses it to build
  the removal variants of each resource.
- `semantic.Conditions` in `internal/semantic/conditions.go` (issue #10, PR #20)
  classifies the guards of a reference for the `conditional-target` and
  `dependency-cycle` rules.

This note records where the two differ, what each caller needs, and what
merging them would change. The short answer is that **a merge changes
validation results for some templates**. No template in the smoke, semantic
or full corpus is affected, but each difference below has a small template
for which one caller's answer would change. The merge is on hold until the
decisions in [Open decisions](#open-decisions) are made.

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

## Which shape to keep

`internal/conditions` should be the single model:

- It is already a standalone package with a documented contract and two
  possible callers in mind. The semantic model is an unexported part of the
  pass's `Model`.
- It models a compared expression as a finite-domain variable. Exclusivity
  and `AllowedValues` are properties of the variable's domain.
  `semantic.Conditions` gets the same result by enumerating every bit
  pattern of its atoms and then filtering out infeasible ones, plus a separate
  rule for a parameter compared with every allowed value. With *k* literals
  per expression, the finite domain has *k* + 1 choices where the atoms have
  2ᵏ patterns.
- `Combinations` is the more general query. `Classify` is a few lines on top
  of it (see above). The reverse is not true: classification can't produce
  the rows that removal variants report.

From `semantic.Conditions` it should take:

- Compiling every condition once, in name order, and recording cycles. The
  current on-demand compilation makes a cyclic template's answers depend on
  which resource asked first (row 4).
- The `Truth` type and `Classify`, and JSON tags on `Literal`, so
  `semantic.Guard` can become an alias and the output stays the same.
- Constant folding, which lets queries over constant conditions skip
  enumeration. It changes no answer, but it does lower the variable count
  that the limit is measured on. See row 5.

## Effect on the corpus and tests

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

So a merge can keep every corpus result and every existing test. It can't
keep every result for every template, because for each of rows 1–6 one
caller's answer has to change.

## Open decisions

For each row, the merged model has to pick one behaviour. These are the
recommendations, and the results each would change:

1. **`AllowedValues` scope.** Recommendation: restrict `String` and `Number`
   only, as `internal/conditions` does, until the AWS-specific types are
   checked against CloudFormation. The CloudFormation documentation lists
   `AllowedValues` as a constraint for `String` and `Number`. For SSM types,
   the input is the Parameter Store key and `Ref` returns the stored value, so
   restricting the `Ref` by `AllowedValues` is probably wrong there. No
   direction is safe for both callers. Restricting too much can turn a
   `conditional-target` warning into an error and hide a removal. Restricting
   too little can turn an error into a warning and report a removal that
   CloudFormation can't reach. Changes: semantic results for templates that
   compare an AWS-specific parameter with `AllowedValues` in a condition.
2. **Non-scalar `AllowedValues`.** Recommendation: ignore the list, as
   `internal/conditions` does. Changes: `-checks semantic` results for
   templates the schema rejects. Today such a parameter makes every
   classification that compares it `Unknown`.
3. **`Fn::And` and `Fn::Or` arity.** Recommendation: unknown, as
   `internal/conditions` does. One rule then covers every shape CloudFormation
   rejects. Changes: `-checks semantic` results, and semantic diagnostics
   listed next to a schema error, for templates the schema rejects.
4. **Cycles.** Recommendation: compile every condition in name order, as
   `semantic.Conditions` does. Changes: removal-variant results in
   `-checks structural` (the default) and `-resource` mode for templates with
   cyclic conditions. The schemas don't detect those cycles, and
   CloudFormation rejects them.
5. **Limits.** Recommendation: measure every query in assignments. Keep 2¹⁴
   for `Combinations` and `Satisfiable`, counted as today. Use 2¹⁶ for
   `Classify`, counted after folding, and count a parameter with
   `AllowedValues` as the values compared plus one for the rest. An
   expression with *k* literal atoms then has at most *k* + 1 ≤ 2ᵏ choices,
   so `Classify` decides every query the 16-atom rule decides today. (Today
   `internal/conditions` counts every allowed value: a parameter with 100
   allowed values compared once is 1 atom but 100 choices.) Changes: semantic
   queries with more than 16 atoms but at most 2¹⁶ assignments become decided
   rather than `Unknown`. Neither the folding nor the reduced count may apply
   to `Combinations` while its 2¹⁴ results are pinned.
6. **Number spelling.** Recommendation: decode once with `UseNumber` in
   `validate.Check` and pass that template to both callers, so `"1"` and
   `1.0` are compared the same way everywhere. CloudFormation's own spelling
   of `1.0` hasn't been checked. Changes: semantic results for conditions or
   allowed values that spell a number differently from its `float64` form.

## Migration plan

Once the decisions are made:

1. Make each decision as its own commit, in the model whose behaviour
   changes, with a test that pins the new answer. Run the corpus before and
   after each one. Reviewers then see every result change separately from
   the move.
2. Add to `internal/conditions` what the semantic pass needs: compiling
   every condition in `New`, `Cycles()`, `Truth`, `Classify`, and a small
   formula type for the edge and cycle rules (a disjunction of guard
   conjunctions, and a conjunction of those). Add JSON tags to `Literal`.
   Port the cases from `semantic.TestClassify` and the cyclic case from
   row 4 to `conditions_test.go`.
3. Switch `internal/semantic` to `*conditions.Model`. Make `semantic.Guard`
   an alias of `conditions.Literal`, so `Diagnostic` JSON doesn't change.
   Delete `internal/semantic/conditions.go`. The rule tests in
   `check_test.go` stay as they are.
4. Check that nothing changed. Run `go test ./...` and `./scripts/corpus.sh
   test`, and compare the semantic diagnostics and removal variants of every
   template in `.corpus/normalized` before and after step 3. The comparison
   should be byte-identical.
5. Update `docs/semantic-validation.md` and the README's AWS::NoValue section
   to point here, and remove the note that a change has to be made in both
   models.
