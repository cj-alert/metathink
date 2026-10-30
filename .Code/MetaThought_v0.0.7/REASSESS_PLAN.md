# Saved assessment prompt comparison

Authorized October 27, 2026: change one scoring instruction and reassess the saved experiment; do not rerun the hive.

## Procedure

1. Preserve source config and original experiment. Change only prompts.assess to check claims against authorship, order and operation type, distinguishing real reciprocal C/E from synthesis. Keep all criteria and numerical anchors unchanged.
2. Select saved meta-drone assessment calls by subject from the existing transcript. For this run use v0.0.7d trial:2, original calls23–25. Reuse their exact operational user messages, model and reasoning setting. Replace only the developer scoring instruction. Existing earlier scores remain in the original payloads; do not substitute newly generated profiles into later calls.
3. Execute the selected calls once through the existing Meta client and assessment parser. Preserve new requests, raw responses, scores, usage and errors in a fresh version-letter folder. Open no original database for writing, and call no hive or Queen. No automatic retries.
4. Compare each new reciprocal score and cited evidence with its original assessment and actual contribution chronology. Separately report recognition of false authorship/order and recognition that synthesis is not a later individual C/E turn. Preserve other criterion scores and uncertainty. Do not manufacture a desired score or claim improved hive behavior.
5. Record rejections, cache and usage-derived costs from returned telemetry. Save the comparison under .Analysis, update the design guide/decision record and session notes. One reassessment batch is the acceptance observation; do not repeat merely to obtain a preferred answer.

## Small execution addition

The existing CLI has no assessment-only command. A narrow reassess route reuses MetaClient, the existing diagnostic logger and ParseAssessment to replay saved assessment requests. It cannot delegate hive work. This is an experiment helper, not a new optimization loop. Compile the addition and directly exercise it on the authorized saved calls; no broad test or hive run.
