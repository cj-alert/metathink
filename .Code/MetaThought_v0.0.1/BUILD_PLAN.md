# MetaThought v0.0.1 build process

Scope: simplest sequential persistent hive plus full public Decisions types through Meta. OpenRouter/native Decisions not enabled.

1. Load config.json; all prompts, schema/index DDL, model IDs and limits live there. Read credentials from the existing key file at call time; never copy them.
2. Initialize state.db and debug.db in the selected flat run folder. Keep hive and meta-queen records/logs separate.
3. Plan a contract from explicit objective/intent/context/criteria. Run only a selected contract file; selecting it is the caller confirmation.
4. Find an existing team by fit or collect two explicit commitments; persist teams/membership and request obligations. Each active drone contributes then expands. Members may invite others and direct information to any drone.
5. Queen evaluates candidates against configured criteria, guides, and collapses when the result or remaining uncertainty should be returned. Fresh requests append obligations.
6. Test Decisions parsing/transport and real SQLite persistence with local deterministic fixtures; build executable; then run one small named live Meta smoke experiment if configuration is available. Do not claim novel solutions or calibrated scores from smoke tests.
7. Inspect SQL/log summaries for the experiment; save result and analysis, update design decisions/session notes. Preserve failed outputs and fix owning prompts/code before rerunning a new named experiment.

Acceptance: executable reports 0.0.1; init owns schema; full Decisions request/answer variants have tests; team request traverses commitments, multiple drones and Contribute/Expand to returned result; database restart preserves teams and obligations; log reads/writes and provider failures; no keys in logs. Any native-only Meta capability gap is explicit.

Clyde correction: arbitrary accounting/limit checks are forbidden. No token, call, round, context-size, transport-duration/byte, or JS-duration quotas. Retain only behavior-critical parsing/storage/team rules and operator cancellation. Usage is logged without gating inquiry.
