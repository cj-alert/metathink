# MetaThought v0.0.4 build process

Authorized October 27, 2026: implement the minimal meta-queen GA platform; report readiness before any complete live evolution test. Existing versions and experimental outputs remain preserved.

## Implementation sequence
1. Add an evolution controller around the existing hive: persistent method population, atomic mutation, compatible whole-instruction crossover, retest, evidence-guided parent selection, and queen return. Reuse Meta and full Decisions; no new dependencies or accounting quotas.
2. Persist functional continuation state and each method/trial using ordinary IDs and SQLite transactions. Keep each candidate hive in distinct flat files in the campaign folder. Log all application SQL and controller/model events under mq diagnostics; never expose debug logs to actors.
3. Add evolve and evolve-resume CLI commands and launcher, configurable prompts/schema/settings, current version identity and usage documentation.
4. Run deterministic local tests with substituted model responses covering mutation, crossover, actual hive delegation, fixed evaluation context, no diagnostic leakage, selection from prior evidence, refusal/failure handling, and resume without replaying completed trials. Preserve existing tests.
5. Build the executable, verify version, review the change, and report ready. Do not launch live model experiments or the complete live evolution loop until the user asks.

## Experimental meaning
Keep the original contract and reference, explicit comparative criteria, base models and unselected behavioral instructions stable. Crossover transfers one whole compatible instruction between selected parents. All methods and evidence remain stored; parent selection does not erase unselected methods. No claim of improvement follows from score alone. The controller may return no demonstrated improvement.
