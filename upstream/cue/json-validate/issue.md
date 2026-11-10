# Draft: encoding/json: Validate misses zero matches for required-field alternatives

Status: ON HOLD. Not approved for upstream submission.

Expected: all three cases reject; exactly one of A and B is required. The original investigation reported that viaJSON is accepted on v0.17.1, v0.18.0-alpha.3 and master ffb2adbe9680; both is rejected. Direct rejects on v0.18, but was also accepted on v0.17.1. This leaves a documented gap for IAM policies supplied as JSON strings. Related historical issues mentioned in the investigation: cue-lang/cue#2395 and #2309. Recheck whether a new issue or follow-up is appropriate.

## Reproduction

See `repro.cue`. Run `cue export repro.cue -e NAME` separately for direct, viaJSON, both.

The cross-version claims above come from the original 2026-10-14 investigation. See `validation.txt` for the version rerun while preserving this draft. Recheck versions and duplicate issues before submission.
