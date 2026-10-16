# Draft: evaluator: embedding matchN appears to reopen a definition

Status: ON HOLD. Not approved for upstream submission.

The original investigation reported that embedded accepts the extra field while outside and plain reject it, on v0.17.1, v0.18.0-alpha.3 and master ffb2adbe9680. Expected: the definition should retain its unknown-field check. cfn-cue already uses close({...}) & matchN(...) as a workaround. Confirm intended embedding semantics with maintainers before asserting a bug.

## Reproduction

See `repro.cue`. Run `cue export repro.cue -e NAME` separately for embedded, outside, plain.

The cross-version claims above come from the original 2026-10-14 investigation. See `validation.txt` for the version rerun while preserving this draft. Recheck versions and duplicate issues before submission.
