# MetaThought v0.0.7

A sequential Go test bed for shared thinking, using Meta, Goja and SQLite. Source and runtime files stay flat inside their version or experiment folder. Earlier versions and generated experiments remain unchanged.

This increment fixes base answer selection and interruption handling, and gives method inquiries the same confirmed-contract/drone-scoring ownership as base inquiries. It does not establish novelty, emergent topology or optimizer effectiveness.

**Focused local checks and the confirmed live sequence are complete.** All 15 local test groups and 26 subcases passed with zero HTTP attempts. The subsequent live campaign completed a mutation and child trial, but its claimed reciprocal improvement was unsupported by the recorded sequence. See the [live report](../../.Analysis/10-27_MetaThought_v0.0.7_Live_Test.md). Commands below document use; they do not authorize another run.

## Build and configuration

Go 1.27 is pinned in go.mod. Goja and modernc.org/sqlite plus their required dependencies are approved; no new dependencies are introduced. Build.ps1 defaults to -Step build: format production Go sources, print the actual SDK version, and compile MetaThought.exe. It does not execute the app or tests. VCS stamping and CGO are disabled; no Git is used. -Step test and -Step all remain available only for later authorized use.

~~~powershell
pwsh -NoLogo -NoProfile -File .\Build.ps1 -Step build
~~~

config.json holds prompts, schema/indexes, models, key-source settings, initial chromosome pairs and contract draft seeds. Spark1.3 Contributor handles ordinary drone/routing work; Wonder1.0 Standard handles Queen and Meta-Queen reasoning. Credentials come from the configured environment variable or original key file and are excluded from prompts/config snapshots/logs. Provider errors retain their text except any echoed credential.

There are no arbitrary token, call, round, context, message-size, transport-duration or JavaScript-duration quotas. Scores are not budgets. Ordinary parser checks, commitments, database transactions and caller cancellation protect the actual work. Diagnostics observe usage; they never control inquiry.

## One confirmed contract per inquiry

Base and meta inquiries use the same contract format: objective, intent, context, intended return and named scoring elements. Each element defines Meaning, Zero and One, an optional exact target and optional weight. Targets retain =, >, >=, < or <=. Config criteria seed drafting only; execution uses the reviewed text. A target is never an actual score.

~~~text
# OBJECTIVE
The result sought.
# INTENT
Why the result is needed.
# CONTEXT
Relevant facts, or None.
# RETURN
The useful return, including unresolved uncertainty.
# CRITERIA
## criterion_name
Meaning: What is assessed.
Zero: What 0 means.
One: What 1 means.
Target: none
Weight: none
~~~

The example is syntax, not an adopted criterion. Missing/unknown/duplicate required structure fails explicitly. Legacy prose contracts are not replaced by config defaults. Preserve old databases with their own executables; v7 initializes new schema rather than migrating experiments.

### Base inquiry

After execution is authorized, Run-Experiment.ps1 defaults to draft: it initializes a fresh folder, drafts contract.md and stops. A separate run requires both the reviewed file and -Confirm.

~~~powershell
pwsh -NoLogo -NoProfile -File .\Run-Experiment.ps1 -RunFolder 'C:\path\.Experiments\MetaThought_v0.0.7a - Base inquiry'
pwsh -NoLogo -NoProfile -File .\Run-Experiment.ps1 -RunFolder 'C:\path\.Experiments\MetaThought_v0.0.7a - Base inquiry' -Stage run -Contract 'C:\path\.Experiments\MetaThought_v0.0.7a - Base inquiry\contract.md' -Confirm
~~~

The hive finds teams by fit, ignoring capacity. At least two actual commitments form a team; membership persists and may overlap. Members run Contribute/Expand, can invite peers and direct informational offers, and synthesize candidates. No founding drone keeps authority. This sequential, shared-context starting shape does not prove organic emergence.

### Meta inquiry

The method contract is separate from the saved task contract. Run-MetaExperiment.ps1 compares one proposed variant; Run-Evolution.ps1 maintains a campaign with parent selection, whole-instruction mutation/crossover, retesting and return. Both default to Draft and stop at meta-contract.md. Drafting writes no active campaign state or controller configuration; its diagnostic log is retained when Start later uses that folder.

~~~powershell
pwsh -NoLogo -NoProfile -File .\Run-Evolution.ps1 -RunFolder 'C:\path\.Experiments\MetaThought_v0.0.7b - Method inquiry'
pwsh -NoLogo -NoProfile -File .\Run-Evolution.ps1 -RunFolder 'C:\path\.Experiments\MetaThought_v0.0.7b - Method inquiry' -Stage Start -BaselineFolder 'C:\path\.Experiments\MetaThought_v0.0.7a - Base inquiry' -MetaContract 'C:\path\.Experiments\MetaThought_v0.0.7b - Method inquiry\meta-contract.md' -Confirm
pwsh -NoLogo -NoProfile -File .\Run-Evolution.ps1 -RunFolder 'C:\path\.Experiments\MetaThought_v0.0.7b - Method inquiry' -Resume
~~~

Use the same Draft/Start options with Run-MetaExperiment.ps1 for a single comparison. -BaselineRequestID selects the saved completed task request. The optional -PriorAssessment belongs only to that single-comparison runner. Existing outputs are preserved; invocation stdout, stderr and exit code receive distinct filenames.

The confirmed meta text is saved in mq_inquiries. Drones explicitly ACCEPT/DECLINE that shared inquiry; two commitments establish its evaluation team. Their chromosome pairs supply perspectives without permanent roles. Each committed drone scores a method/trial from the confirmed meta criteria and actual operational evidence. The meta-queen reads the separate profiles and chooses useful experiments or return. The campaign reference is characterized before the first action. This minimal evaluating team is not yet a full recursive Contribute/Expand team topology.

Resume loads the saved campaign config and meta contract, without accepting replacement contracts. No draft defaults silently override either inquiry. A changed governing objective requires a new reviewed inquiry. Original task results, contracts, trials and unsuccessful methods remain available.

## Scores, evidence and return

Base candidate and meta method assessments use one parser and this plain-text reply format:

~~~text
# criterion_name
Score: 0.4
Evidence: The observation supporting this assessment.
Uncertainty: What remains unknown or could change the judgment.
~~~

The number is a syntax illustration. Every declared element requires a finite actual score in [0,1], plus evidence and uncertainty. Missing/malformed values are not zero and are not clamped. A valid low score is an assessed outcome. Each complete drone vector is saved in one ordinary transaction; interruption cannot expose half a vector. Completed peers are preserved.

Queen judgment receives the confirmed contract, attributed operational trajectory, scores, offers, objections and uncertainty. No average, vote, count or fixed critical-mass formula determines return. A return may honestly be UNMET or UNCERTAIN.

Base collapse has a mutable behavioral instruction and a fixed configurable collapse_format. Its reply contains # DECISION (RETURN/CONTINUE), # CANDIDATES (comma-separated saved candidate IDs for RETURN, NONE for CONTINUE), # CRITERIA (MET/UNMET/UNCERTAIN), and # REASON. Return IDs must identify candidates belonging to this request. One selected answer is returned verbatim; multiple selected answers use deterministic ID labels. The latest synthesis is not substituted for the Queen's choice.

The return review and request finalization are saved atomically. Resume recovers a saved valid return before making new model calls. Stored selection, criterion status and rationale remain visible in the result. Ordinary continuation otherwise starts after saved work. There is no automatic provider retry or hidden re-execution of a completed return.

Meta scores live in mq_assessments with inquiry, subject, drone and criterion attribution. Meta-queen comparison/return prose interprets evidence; it does not replace drone scores with new grades. Default method criteria are draft examples with 0/1 anchors, not a mandatory universal rubric. Task fidelity references the actual confirmed task contract.

## Direct command equivalents

~~~powershell
.\MetaThought.exe plan -config config.json -dir BASE_FOLDER
.\MetaThought.exe run -config config.json -dir BASE_FOLDER -contract REVIEWED_TASK -confirm
.\MetaThought.exe resume -config BASE_FOLDER\config.json -dir BASE_FOLDER -request-id 1
.\MetaThought.exe meta-plan -config config.json -dir META_FOLDER
.\MetaThought.exe experiment -config config.json -dir META_FOLDER -baseline BASE_FOLDER -meta-contract REVIEWED_META -confirm
.\MetaThought.exe evolve -config config.json -dir META_FOLDER -baseline BASE_FOLDER -meta-contract REVIEWED_META -confirm
.\MetaThought.exe evolve-resume -dir META_FOLDER
.\MetaThought.exe status -config config.json -dir RUN_FOLDER
.\MetaThought.exe analyze -config config.json -dir RUN_FOLDER
.\MetaThought.exe decide -config config.json -dir RUN_FOLDER -request decisions_smoke.json
.\MetaThought.exe eval -config config.json -dir RUN_FOLDER -script calculation.js
~~~

run -parent ID adds an obligation rather than cancelling its predecessor. A new -result filename preserves previous exports. Completed single-comparison runs have no separate resume command; campaign evolution supports explicit resume.

## Observation and provider boundaries

state.db holds operational records; debug.db holds profuse developer-only diagnostics. Agents never read the diagnostic database. Every application SQL access logs operation/table, outcome, duration and affected/read rows; transaction outcomes and model/parser phases are logged. Logger sink inserts are excluded from recursive logging. mq operational tables and mq_log remain separate and indexed for inquiry, subject, scorer, criterion, status and time analysis.

The complete public Decisions request/answer interface remains available: predicate, typed choice, score distributions, confidence and refusal. Its generic ordered-level score semantics are separate from application criterion assessments. Meta estimates are not assumed calibrated or identical to native Decisions. Native cache mode/TTL/breakpoints remain explicitly unsupported; unavailable usage counters remain null. OpenRouter and native Decisions backends remain deferred. Meta usage is observed; dollar costs are not invented as provider-returned charges.

Goja exposes no filesystem, network or process APIs; it supports caller cancellation but is not a heap-memory sandbox. A contribution can explicitly request JavaScript calculation. The runner is sequential; concurrent workers must not share one run folder.

## Evidence and remaining scope

The focused local suite passed on October 27: 15 top-level tests and 26 subcases, package time 28.349 seconds, exit 0, zero HTTP attempts. Run-FocusedTests.ps1 compiles all production sources with only v007_*_test.go and saves unique JSONL/stderr/command receipts. See [LOCAL_TEST_PLAN.md](LOCAL_TEST_PLAN.md) and the [focused test report](../../.Analysis/10-27_MetaThought_v0.0.7_Focused_Local_Tests.md). No production change or rebuild was needed. Historical fixtures retain older APIs/protocols and were excluded; this is not full-suite acceptance. Simulated replies establish local mechanics, not live model effectiveness.

The canonical every-prompt/per-element scoring requirement remains broader than this increment. Candidate and method assessments are covered; planning, atomic C/E work, routing, mutation and other auxiliary outputs still need meaningful local scoring. No automatic evaluator-of-evaluator chain is introduced. Organic subteams, reliable reciprocal influence, cross-campaign method retrieval, strategy self-improvement, autonomous drift resubmission and discovered-information invalidation remain incomplete or unproven. No SIDS hypothesis or worldwide novelty claim has been established.

Use a fresh compatible v7 baseline for controlled comparisons. Imported historical evidence may involve different runtime mechanics; preserve it, but do not attribute a cross-version difference solely to a chromosome change. Current fixed assessment/reply protocols accompany imported behavioral instructions, without silently rewriting the saved behavior or task contract.

Show-QueenPrompts.py prints configured templates and draft criteria for inspection without model/database access. It does not reconstruct a historical rendered call. Analyze-SavedExperiment.py and Analyze-Evolution.py remain developer-only evidence readers; their historical reports do not replace operational scores or current contract authority.

## Completed live test and analysis

[LIVE_TEST_PLAN.md](LIVE_TEST_PLAN.md) records the baseline-then-method process and its influence/rejection/cost observation rules. Clyde confirmed the exact locally authored [base](../../.Discussion/10-27_MetaThought_v0.0.7_Base_Contract_DRAFT.md) and [meta](../../.Discussion/10-27_MetaThought_v0.0.7_Meta_Contract_DRAFT.md) contracts in D87; their DRAFT filenames are retained. v7b returned, v7c failed on a wrong-rubric assessment, and v7d completed after the minimal configurable [rubric clarification](META_CRITERION_RECOVERY.md). No production Go or binary change was required. The report distinguishes useful development from false reciprocal claims. No further live call is authorized by this README.

The developer readers run from this source folder and write new report files; they never call a provider or write an experiment database. Keep Analyze-SavedExperiment.py and Analyze-Evolution.py together: the base reader shares the campaign reader's usage/failure extraction. Supply the [verified pricing snapshot](../../.Analysis/10-27_MetaThought_v0.0.7_Live_Pricing.json). Report actual drone assessments, operational influence evidence, separate rejection classes and published-rate per-call token cost estimates; missing usage remains unknown. Imported baseline cost is excluded from the campaign subtotal and shown separately. Logs remain developer-only.

The reporting repair passed one offline synthetic fixture (exit 0, zero network calls); see the [preparation and proof report](../../.Analysis/10-27_MetaThought_v0.0.7_Live_Preparation.md). The later live sequence is documented separately in the [live report](../../.Analysis/10-27_MetaThought_v0.0.7_Live_Test.md). Run-Analysis.ps1 wraps these readers with exact runtime/command/exit receipts; Base and Evolution modes write new reports, and Status performs a read-only query. It never calls Meta or changes experiment databases.


## Reassess saved scoring calls without running the hive

[Run-Reassessment.ps1](Run-Reassessment.ps1) takes RunFolder, Transcript and Subject. It builds into a fresh folder, then invokes the narrow reassess command. The command selects captured meta-drone assess requests for the subject, replaces only the configured developer assessment instruction, and reuses MetaClient, the diagnostic logger and ParseAssessment. It returns before any hive or campaign startup. The saved operational user messages, model settings and prior-score context remain unchanged; original databases are never opened for writing. No automatic retry or new working-state schema is added.

The D88 run replayed trial:2 assessment calls23–25 from v0.0.7d. All three caught the missing later individual feedback; reciprocity0.85 became0.35/0.30/0.40. This is a saved-case result, not general scoring validation. The [report](../../.Analysis/10-27_MetaThought_v0.0.7e_Reassessment.md) preserves all scores, limitations and the approximately$0.00485 cost. Source config differs only in prompts.assess; the helper is a diagnostic execution route. The original source-folder executable remains the prior build; this launcher compiles the helper into its new output folder.
