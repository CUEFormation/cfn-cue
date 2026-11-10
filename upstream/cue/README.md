# Upstream CUE reports — on hold

These are draft reports and reproductions collected from cfn-cue work on 2026-10-14. They are parked here for review, not approved for submission to cue-lang/cue. Creating or merging this project PR does not authorize upstream submission. Wait for Joel’s explicit go-ahead for each report. There is no automatic submission script or workflow.

## Drafts

- `optional-field-performance/issue.md`: existing full report, preserved with its original reproducer and historical measurements. Optional recursive fields inside disjunctions are much more expensive than equivalent pattern constraints; the v0.18 formatter introduces the slow spelling.
- `matchn-closedness/issue.md`: embedded matchN appears to reopen a definition.
- `json-validate/issue.md`: JSON-string validation misses an exactly-one constraint when neither field exists.
- `disjunction-errors/issue.md`: a third alternative hides the useful nested error in v0.18.

The last three are working notes reconstructed from the investigation thread, not polished submission text. Original investigation: [Explore IAM Policy Validation in CUE](t3-thread://v1/c16fc6be-0f47-4c35-91a0-55f55427dfec).

## Before submitting, if authorized

1. Choose which reports to submit; the original investigation prioritized the diagnostic regression.
2. Recheck against the then-current CUE release/master and search for duplicate issues. Historical claims about “latest” in the performance draft refer to October 2026, not submission day.
3. Review titles, causal claims, expected behavior and reproducer wording. The performance example does not itself enforce exactly one function key, despite its original comment.
4. Submit only after explicit approval; record upstream links here afterward.

## Reproducing

Use a scratch copy of each directory when running formatters. Each directory has its own CUE module. For the performance pair, run `cue vet -c optional.cue` and `cue vet -c pattern.cue` separately; combining them would combine their definitions. `repro.txtar` also includes the formatter comparison.

For the other cases, run `cue export repro.cue -e NAME` separately for the expressions listed in each note. `validation.txt` records a local rerun on the installed CUE version; these files are observations, not passing regression tests. Intentional failures are expected. To compare v0.17.1, change the scratch copy’s module language version to v0.17.0.

No generator changes, generated schemas, or corpus work are included in this branch. The original temporary files were retained.
