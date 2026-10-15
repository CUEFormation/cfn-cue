# Corpus and service-rule validation, 2026-10-14

These results preserve the final survey and timing evidence from the corpus implementation thread. The survey covers 1,101 templates in us-east-1, with a six-second deadline and a 2 GiB soft memory target. It encountered 333 resource types and no CUE timeouts.

`summary.json` records the final counts, coverage and fingerprints. `run.json` records the inputs and selection. `comparison.md` and `comparison.json` compare the final build against the pre-service-rule baseline (which already included scalar, grammar, dynamic-reference and format work).

The generated CUE fingerprint, manifest hash and validator binary hash matched commit 9fea8fe3, before integration of main’s definition-pruning change from PR #5. Fresh generation of all 45 regions and 1,866 resource schemas matched that commit byte for byte. After integrating PR #5, schemas were regenerated and acceptance checks rerun; the historical survey and timing numbers below are not new measurements of the integrated build. The final acceptance suite is recorded in the PR and CI.

## Outcomes

Six templates previously accepted by CUE but rejected by cfn-lint are now rejected by both: four S3 ACL cases, one Lambda ZipFile runtime case, and one Route 53 case. The CUE-only rejection count stays at 18. A SAM fixture with multiple invalid resources changes from rejected to unsupported because resource iteration can select different failures first. These counts are comparison evidence, not proof of deployability.

## Paired timing

`paired-timings.json` contains 2,310 pairs: 770 affected templates, three rounds, baseline (`pre`) and final (`2a`) run back to back in shuffled order with seed 7. Values are the validator's elapsed milliseconds. The original timing run used a 30-second validator timeout; it is distinct from the six-second full survey.

Taking each template’s median across its three rounds, then the median across templates, gives 63 ms before and 67 ms after. The median paired difference across all measurements is 3 ms; total measured CUE time increases by 8%. The largest per-template median increase is 88 ms. These are local measurements, not a cross-platform performance guarantee.

Seven costly rules remain disabled: E3042, E3047, E3048, E3638, E3639, E3690 and E3691. The final build applies 42 rules across 32 resource types.

## Deferred work

- Encode the seven disabled rules more cheaply before enabling them.
- Support nested Fn::If around whole Properties blocks.
- Add semantic/reference resolution and condition-aware required-property checks separately.
- The previously observed GroundStation DataflowEndpointGroupV2 extraction failure was investigated in [issue-12-field-rule-placement.md](issue-12-field-rule-placement.md). It was not reproduced from committed source; the report records the evidence and the regression test.
- Upstream CUE issue drafts are parked separately in draft PR #6 and remain on hold.
