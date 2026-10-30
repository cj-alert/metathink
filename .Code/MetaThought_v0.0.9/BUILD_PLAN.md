# v0.0.9: observe organic inquiry teams

Authorized October 28, 2026. Implement P17 from the frozen Queen handoff and run one early behavior experiment, stopping for reevaluation when multiple teams actually contribute. Preserve all prior versions and experiments.

## Process

1. Copy the prior version's reusable Go transport, SQLite diagnostics, JS and Decisions interface. Keep all third-party dependencies unchanged. Add one inquiry runner; do not retrofit historical campaign state.
2. Prepare a new run from the frozen v8f question, accepted intake and handoff. Apply the user's explicit textual change 'two drones' to 'multiple drones' to the run's handoff/role copies; preserve original files separately and never replay Queen calls. Change only the new version's downstream instructions/schema plus its initial six-drone population and version metadata.
3. Expose a shared operational board. Every drone cycles through Contribute and Expand using the same action interface: propose an inquiry and explicitly commit, join an inquiry and explicitly commit, contribute, connect inquiries, or pass. No roles or permanent first-receiver authority. Multiple commitments make a team; members may overlap, and questions remain tied to the original request.
4. Save committed actions, members, work, and actual local 0-to-1 scores in indexed SQLite through the existing logged Store. All provider requests/replies and SQL access summaries remain in the separate debug database, never agent context. Replies use explicit plain-text sections, not model JSON.
5. The experiment observer stops between atomic calls when multiple inquiries each have multiple explicitly committed members who have contributed. This is the user's observation boundary, not an instruction, reward or general collapse rule. Stop also on actual failure/cancellation or a complete pass with no activity. No token/call/time quotas and no automatic retries.
6. Focused local simulated tests cover action parsing/scores, frozen handoff ingestion, overlapping commitments/links, and observed-team stopping without a Queen call. Build once after the relevant edits; run the one live experiment; inspect saved chronology, commitments, connected inquiries, errors and usage. Preserve failure rather than manually repairing generated text.

## Boundaries

This first experiment observes team formation and initial thinking only. It does not complete the SIDS request, validate novelty, claim verified citations, or conduct medical trials. Unanswered clarification questions remain explicit. The shared board includes operational work only. Local action scores describe contribution relevance and inquiry value; they do not replace the parent's hypothesis criteria. The frozen Queen intake and handoff are not rerun. No new dependencies, hashes, matching ledgers, fixed role hierarchy, forced number of proposals, or fabricated teams.
