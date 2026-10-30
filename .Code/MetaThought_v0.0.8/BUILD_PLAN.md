# v0.0.8: question → Queen proposal → user confirmation → drones

Authorized October 27, 2026. The Queen must derive objectives, scoring elements, target scores and evaluation approach from the user’s question through real Meta calls. Codex is only the front end for those exchanges and must not rewrite prompts or generated plans.

## Boundary

All existing prompts, chromosomes and Decisions prompt strings are preserved from v0.0.7. The new intake instructions are the Founder’s exact sentence, stored as configuration and forwarded verbatim alongside the exact question. No supplied default objective, criterion or target is sent in raw-question planning. No manual editing, cleanup or substitution of a Queen reply.

## Sequence

1. The front end accepts the exact user question and optional exact feedback on a previous Queen proposal.
2. A shared Queen planning function calls the configured high-reasoning Meta model using the existing unchanged contract prompt. It sends the question, Founder-authored intake instruction, and any previous proposal/user feedback without rewriting them.
3. Save the complete model response unchanged, including malformed responses, and record provider diagnostics. Parse the existing plain-text contract fields for usability. Objectives/criteria/targets/meanings and anchors come from that response. Evaluation instructions can be expressed in its context and criterion meanings/anchors; no new model-authored JSON or scoring formula.
4. Stop and show the exact proposal for confirmation. Drafting creates no hive obligations, drones, team commitments or experiment. A revision calls the Queen again with the user’s feedback and retains both drafts.
5. Only the user’s explicit approval starts work with that exact saved contract. Reuse the existing confirmed-contract runner and persisted assessments; do not add hashes, approval ledgers, matching gates or new working-state tables.

## Acceptance

Focused local checks prove that raw question/instructions/feedback reach the Queen unchanged, legacy preset objectives/criteria are absent, the returned draft is preserved verbatim, malformed/refused replies cannot start work, and confirmation remains required before base/meta execution. Check the existing run contract path receives the selected generated text without replacing its criteria. Compile once after the final code change; run the focused checks with injected replies and no network.

After the user supplies the exact first question, make a real Queen draft call and present it unchanged for review. Do not start drones before that confirmation. Existing scoring incompleteness and the prior Queen review-format failure are separate; do not expand this build into those repairs.

## First fresh test (D91)

Use the exact original question: Find one or more novel, testable hypotheses to solve/cure Sudden Infant Death Syndrome.

The raw-question test forwards this question without injecting legacy sensor objectives, method criteria, or prewritten target values. The Queen’s own proposal is the review artifact. Earlier SIDS target examples remain in the design record and differences must be visible during user confirmation. No existing experiment is resumed, replaced or deleted. New planning artifacts live in a fresh version-letter experiment folder; no operational hive database is created before confirmation.

## Planning observation

After the planning call, Run-Analysis.ps1 -Mode Planning reads only the existing diagnostic database through the saved receipt extractor. It records request/reply, model, usage, errors and a saved-rate cost estimate; it checks operational-state absence without opening that database. The proposal itself is read directly from the saved file and shown unchanged. No analysis material enters Queen/drone context.
