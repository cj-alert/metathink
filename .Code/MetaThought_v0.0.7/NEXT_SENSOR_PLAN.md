# One queen-chosen sensor comparison

Authorized October 27, 2026: run the next sensor experiment; let the Meta-Queen choose one change; use the corrected scoring prompt; report plainly whether thinking improved.

## Repeatable process

1. Select the latest completed sensor hive as the saved reference. For this comparison it is v0.0.7d trial2 request1. Reuse its exact task contract, operational history and model settings without rerunning it. Retain the confirmed v0.0.7 method contract.
2. Use the existing Run-MetaExperiment.ps1 single-comparison route. Prepare a controller input from current config.json, selecting the reference state filename through files.state. This only selects existing storage; it does not change the method. Keep the corrected prompts.assess and all compatible target choices unchanged.
3. Let the existing process obtain committed-drone reference assessments, let the Meta-Queen select one target and propose one replacement instruction, run one fresh candidate hive and assess/compare it under the same corrected rubric. No hand-chosen mutation or score, extra optimizer layer, budget gate or working-state table.
4. Preserve generated outputs. Developer-only extraction reads the filenames recorded in saved configurations, including separate controller and hive diagnostics. Compare actual attributed reasoning changes rather than dialogue volume or scores alone. Report useful development, any feedback to an earlier contributor, task quality, disagreement and uncertainty.
5. Report the one comparison, provider/format failures and usage-derived cost; no automatic next experiment. A weak or inconclusive result remains a valid finding.

## This run

Output: .Experiments/MetaThought_v0.0.7f - Queen chosen sensor change
Reference: .Experiments/MetaThought_v0.0.7d - Method criterion focus/trial-2-state.db, request1
Controller input: controller-input.json in the new output folder; only files.state changes from current source config.
Executable: existing verified MetaThought_v0.0.7 source-folder executable; no application rebuild or code change.
Contract: .Discussion/10-27_MetaThought_v0.0.7_Meta_Contract_DRAFT.md, previously confirmed and unchanged.
