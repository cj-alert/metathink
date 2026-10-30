# MetaThought v0.0.7 base recovery and confirmed meta inquiry

Authority: fix the validated v6 issues and apply the same confirmed-contract/drone-scoring approach to the meta-queen. Keep implementation simple and discuss before testing.

## Repeatable implementation process
1. Preserve v6 and all experiments. Copy source into this flat v7 folder without binaries or runtime state. Use approved dependencies only.
2. Make Queen return selection explicit using saved candidate IDs; return the actual selected answers. Persist each parsed drone assessment vector atomically. Persist return judgment and request finalization together, and recover prior durable returns before new inquiry.
3. Add draft-and-confirm flow for the governing meta contract independently of the task contract. Save the confirmed text and use it unchanged on resume; configuration seeds drafts only. No hashes, content comparisons or quotas.
4. Reuse drone contract assessment for methods and operational evidence. At least two committed meta drones form the evaluating team, without permanent roles. Preserve attributed per-element0..1 scores, evidence, uncertainty and disagreement. Meta-queen receives scores/evidence and guides/selects/collapses; no Queen-generated numeric grading or score formula.
5. Correct runtime descriptions and remove fixture-specific governing meta criteria. Keep task and meta inquiry objectives distinct; fidelity refers to the actual confirmed task contract. Keep plain-text replies, full Decisions interface, separate indexed mq records/logs, and SQL access logging.
6. Update CLI/launchers/config/docs and integration paths. No new dependencies or unrelated topology/strategy work. Disclose any unconverted auxiliary prompt-scoring scope.
7. Perform independent source review and production compilation only. Do not run tests, test compilation, application commands, live provider calls, experiments or schema migrations. Stop with the build result and discuss focused testing with Clyde.
