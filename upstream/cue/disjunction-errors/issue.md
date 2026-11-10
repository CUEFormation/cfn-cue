# Draft: evaluator: third disjunct hides nested list-element error

Status: ON HOLD. Not approved for upstream submission.

Both expressions should reject, with a diagnostic identifying a: "x" as incompatible with int. The original investigation reported that v0.17.1 retains that cause for three, but v0.18.0-alpha.3 and master ffb2adbe9680 only report a conflict with the unrelated {c!: int} arm. This is a diagnostic regression, not acceptance of invalid data. In cfn-cue, adding Fn::If as a third Statement alternative exposed it. Checking an individual statement profile remains a workaround.

## Reproduction

See `repro.cue`. Run `cue export repro.cue -e NAME` separately for two, three.

The cross-version claims above come from the original 2026-10-14 investigation. See `validation.txt` for the version rerun while preserving this draft. Recheck versions and duplicate issues before submission.
