# MetaThought v0.0.5

A small sequential Go hive for experiments in shared thinking. The application uses Meta, with plain-text model replies behind the full Decisions request/answer interface. All runtime files stay flat in the selected run folder.

This release corrects the meta-queen completion/context gap exposed by the first live GA campaign. Controller decisions receive attributed operational inquiry evidence and saved assessments. The imported reference is assessed before the first action; solving the task alone no longer defines the method inquiry's return condition. Earlier saved experiments remain separate evidence, and this correction has not yet been tested with live model calls.

## What works

- Fit-based discovery of persistent teams; at least two explicit commitments per request. Membership can overlap and the founder has no permanent authority.
- Atomic Contribute and Expand calls, shared operational contributions, directed information exchanges, and peer invitations.
- Wonder contract drafting, criterion scoring, guidance, and queen-controlled collapse. Each fresh request adds an obligation.
- Explicit continuation of an unfinished request. Completed contributions are preserved; continuation starts new atomic work after the last saved tick without replaying completed contributions.
- Full predicate/choice/score/refusal shapes, typed boolean/string choices, distributions/confidence, weighted zero-based scores, text/image/JSON/conversation/tool evidence.
- Separate state.db and debug.db, indexed hive/meta-queen tables and logs, every application SQL operation summarized, complete provider requests/replies, parsing outcomes, timings and returned usage. Actors never read debug.db. Log-sink setup/inserts are excluded from recursive instrumentation.
- Fresh Goja runtime for optional calculations. A contribution beginning with exactly `# JAVASCRIPT` on its own line runs its following script; the result becomes an operational contribution. The CLI also supports eval.
- A campaign-local retained method population, whole-instruction mutation/crossover, retesting, attributed assessment, and meta-queen-controlled return. Explicit continuation uses the saved campaign configuration.

## Build

Go 1.27 is required. Goja and modernc.org/sqlite (including required dependencies) are approved and pinned in go.mod/go.sum. No C compiler or runtime SQLite installation is required.

```powershell
pwsh -NoProfile -File .\Build.ps1 -Step all
.\MetaThought.exe version
```

Build.ps1 downloads pinned modules through the Go module proxy, runs tests, then builds MetaThought.exe. It disables VCS stamping and CGO. It never runs Git.

## Configure

config.json contains model IDs, endpoint/key-source settings, all system/decision prompts, initial chromosome pairs, the smoke problem and criteria, SQL schema, and indexes. The default models are `muse-spark-1.3-contributor` and `muse-wonder-1.0` (Standard, approved because Wonder Contributor is unavailable in the API). Wonder uses high reasoning; Spark uses low.

Set the API key in the configured environment variable or retain the existing configured key file. The environment value takes precedence. `api_key_file_format` supports raw and single_value_line; the latter accepts exactly one standalone credential line surrounded by explanatory text. Credentials never enter prompts, config snapshots, or diagnostic payloads. Provider error bodies remain intact except if they echo the actual key.

Internal accounting friction is forbidden unless functionally required under a high bar. There are no local token, call-count, round-count, context-record, signal-length, response-byte, HTTP-duration, or JavaScript-duration ceilings, and no corresponding limit checks. The queen decides when to return; cancellation remains available to the operator. Usage measurements are diagnostics only. Protocol validity, database failures, actual team commitments, and cancellation still receive necessary handling. Provider/format failures preserve paused obligations; there is no automatic retry or provider fallback.

## Start an experiment

Choose a fresh `.Experiments/MetaThought_v0.0.5<letter> - <objective>` folder. The script preserves earlier outputs and copies the executable and initialization configuration.

```powershell
pwsh -NoProfile -File .\Run-Experiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.5a - First inquiry" -Stage init
pwsh -NoProfile -File .\Run-Experiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.5a - First inquiry" -Stage decisions
pwsh -NoProfile -File .\Run-Experiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.5a - First inquiry" -Stage plan
```

Review contract.md, then use `-Stage run` to select/confirm it for the hive. `-Stage analyze` produces diagnostic aggregates. Every stage records stdout, stderr, and its exit code; failures print the exact error. A generated contract is never silently edited to make an experiment pass. Fix its owning configuration/prompt and preserve the earlier output.

## Direct commands

Run from a flat experiment folder containing config.json and MetaThought.exe:

```powershell
.\MetaThought.exe plan
.\MetaThought.exe run -contract contract.md
.\MetaThought.exe status
.\MetaThought.exe analyze
.\MetaThought.exe resume -request-id 1
.\MetaThought.exe run -contract next-contract.md -parent 1 -result next-result.md
.\MetaThought.exe decide -request decisions_smoke.json
.\MetaThought.exe eval -script calculation.js
```

Selecting `-contract` is explicit caller confirmation. `resume` continues the same obligation; `run -parent` creates a new one and retains the old. `resume` selects a unique output filename. Source/config must match the database version; v0.0.5 initializes its own schema and does not include migration between experimental schema revisions. Earlier failed development snapshots remain historical.

## Decisions compatibility boundaries

The public request/answer types follow the official Decisions contract, including optional names, per-question refusals, choice types, nested evidence, and usage. Meta estimates are self-reported, not assumed calibrated or equivalent to native Decisions. The adapter renders the same question content and uses explicit plain-text sections.

Meta supports forwarding prompt_cache_key and safety_identifier. Native Decisions cache mode/TTL and explicit breakpoints have no established faithful Meta mapping and return unsupported_feature; they are never silently discarded. Unreported usage details serialize as null, rather than inventing zeros. These are explicit provider differences, not full native wire/behavior parity. One logical request may require one atomic Meta call per question.

## Current scope

This version adds a persistent evolutionary campaign around the existing hive. The meta-queen chooses methods and actions from its campaign-local library, can mutate one instruction, transfer one compatible whole instruction through crossover, retest a method, or return supported findings. The existing single `experiment` command remains available. Retention in a campaign is not promotion into a shared production method library. A/B/mixed population comparisons remain later work. No external search or discovered-information invalidation system is implemented. The sensor problem is a technical smoke case; it cannot establish novelty or validate SIDS hypotheses.

Goja exposes no filesystem, network, or process APIs and supports caller cancellation. It is not a heap-memory sandbox. The application is sequential; do not run simultaneous hive workers against one run folder. No local quota is imposed to substitute for queen judgment.

## Evidence

Automated tests cover real SQLite persistence/isolation/logging, provider and Decisions parsing, typed values, cancellable JS, multiple-drone interaction, fresh obligations, failure preservation, and continuation. Saved live experiment folders retain their exact executable/config and all outcomes. Read the project .Analysis report for measured live status.

## Meta-queen experiment cycle

Use a completed saved baseline and a fresh run folder. The meta-queen reads only the baseline's operational state, selects one configured target through Decisions, proposes one replacement instruction, runs the unchanged contract through the existing hive, and compares both attributed trajectories. The baseline is opened read-only and its model calls are not repeated.

```powershell
pwsh -NoProfile -File .\Run-MetaExperiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.5a - Prompt variation" -BaselineFolder "C:\path\.Experiments\MetaThought_v0.0.1d - No quota hive" -BaselineRequestID 1
```

The direct command is `MetaThought.exe experiment -config CONFIG -dir NEW_FOLDER -baseline BASELINE_FOLDER -baseline-request 1`. Optional `-prior ASSESSMENT_FILE` supplies a previous comparison as operational feedback; the wrapper names it `-PriorAssessment`. Repeating the cycle with feedback is the initial meta-queen loop. The selected baseline may remain the original or a prior candidate; no candidate is silently promoted.

`meta_queen` in config supplies objective, runtime context, problem type, zero-based chromosome index, optional target choices, and explicit comparative criteria. With `targets`, `mq_select` asks Wonder to choose one target through the Decisions interface. Contribute/Expand names address the configured chromosome pair; other names address existing base-hive prompts, such as candidate or collapse. Controller prompts cannot be mutated. Without target choices, `behavior` selects a fixed target. `mq_propose` asks for one plain instruction. `mq_compare` asks for plain-text ratings, attributed evidence, uncertainty, and the next hypothesis. Ratings are observations, not runtime gates. Base hive prompts and task criteria remain those saved with the baseline. The meta-queen uses controller model/connection settings. The candidate preserves the baseline model, reasoning, and endpoint settings; only its credential source, schema/version, controller metadata, and selected instruction are refreshed.

Saved files stay flat: `mq_config.json` (controller), `config.json` (actual variant), original contract, candidate result, exact `comparison.md`, state/debug DBs, executable, stdout/stderr. `mq_experiments` retains the experiment and failure state; `mq_log` retains meta-queen calls and data access. Drones/queens/meta-queen never receive developer debug logs.

A single run can reveal behavior, not prove causality or general improvement. The evaluator is model-based and may be wrong; inspect its cited operational evidence. Provider failures are preserved, never silently retried.

## Reciprocal-influence comparisons

The starting config offers Contribute, Expand, candidate synthesis, and collapse as alternative intervention targets. The meta-queen receives the prior assessment and an accurate description of what each layer can see. The base scheduler and context composition are unchanged.

The comparative criteria distinguish no evidence, one-way uptake, and substantive reciprocal development. Evidence must come from C/E artifacts: A changes B, then a later development by B changes A. Team synthesis, routing edges, acknowledgements, and more turns alone do not establish reciprocity. No round count is forced.

For a cumulative next cycle, use the preceding completed candidate folder as BaselineFolder and its comparison.md as PriorAssessment. Keep the same comparison prompt and criteria. Preserve the original anchor separately; cumulative adaptation is not independent replication or automatic promotion.

## GA campaign

Use `evolve` to start a new campaign from one completed saved request. The baseline is imported as the first method and returned trial without repeating its hive calls. The existing assess phase characterizes that reference under the current fixed criteria before the first action. Its assessment is persisted on the imported method/trial, so completed assessment work is reused on resume. Further methods and trials remain in this campaign's retained population, with parent/donor relationships and exact configurations in operational SQLite state.

```powershell
pwsh -NoProfile -File .\Run-Evolution.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.5a - Evolution campaign" -BaselineFolder "C:\path\.Experiments\MetaThought_v0.0.2a - First prompt variation" -BaselineRequestID 1
```

The launcher requires a fresh folder, copies the executable, and passes the source configuration to `RunEvolution`. The application owns the exclusive `config.json` snapshot. Invocation stdout, stderr and exit code receive timestamped flat filenames. Exact application errors remain in stderr, while stdout retains the returned `EvolutionResult`, including failure state when available. No campaign is started by the build command.

The direct start command is:

```powershell
.\MetaThought.exe evolve -config C:\path\config.json -dir "C:\path\campaign" -baseline "C:\path\completed-baseline" -baseline-request 1
```

The meta-queen chooses the next action through the existing Decisions interface:

- `MUTATE`: select a parent and permitted target, then generate one replacement instruction.
- `CROSSOVER`: select a parent, donor and compatible target; transfer one whole instruction without blending sentence fragments.
- `RETEST`: obtain another trial of an existing method when the evidence warrants it.
- `RETURN`: report a supported methodological conclusion or explain why no informative next experiment is warranted. The task answer alone does not complete the method inquiry. No minimum generation or trial count is required.

`mq_action`, `mq_parent`, `mq_donor` and `mq_target` make atomic selections. `mq_mutate` returns only an instruction. `mq_assess` examines attributed operational records against the fixed comparative criteria, including earlier assessments. `mq_return` reports supported findings. Model-authored JSON is not required, and ratings are not treated as calibrated probabilities. Controller prompts and criteria remain fixed within the active campaign; only permitted method instructions evolve.

Resume with the campaign's saved executable and configuration:

```powershell
pwsh -NoProfile -File .\Run-Evolution.ps1 -RunFolder "C:\path\campaign" -Resume
.\MetaThought.exe evolve-resume -dir "C:\path\campaign"
.\MetaThought.exe status -config "C:\path\campaign\config.json" -dir "C:\path\campaign"
```

`evolve-resume` always reads `RUN_FOLDER/config.json`, regardless of an external `-config` value. It continues the saved campaign; it does not create a new campaign or silently substitute current source prompts. Preserve failed artifacts and use the explicit continuation path. The launcher never replaces a saved campaign executable during resume.

The campaign stores progress in `mq_evolutions`, exact parent/donor method configurations in `mq_methods`, and operational trial evidence plus assessments in `mq_trials`. `status` exposes those identities and states. Query indexes cover active campaigns, ancestry, methods, trial outcomes and timestamps; diagnostic indexes locate evolution/method/trial events. Campaign diagnostics stay in `debug.db`. Each trial uses flat `trial-<id>-config.json`, `trial-<id>-state.db`, `trial-<id>-debug.db`, `trial-<id>-contract.md` and `trial-<id>-result.md` files. Neither campaign nor trial debug logs become agent input. A completed trial reuses its saved result without provider calls.

A campaign can produce useful hypotheses without establishing superiority. One problem or a favorable stochastic trial is insufficient for a general claim. A meaningful change in C/E artifacts matters; synthesized claims of collaboration, more turns, and agreement do not establish reciprocal development. The platform preserves those distinctions while permitting the meta-queen to return an uncertain or negative result.

## Queen prompts and scoring

Run `Show-QueenPrompts.py config.json` with Python 3 to print the exact configured queen templates, task and meta-method rubrics, and Decisions formatting instructions. This read-only developer utility makes no model calls. Runtime method context includes `meta_queen`, a separately named `task_contract`, attributed trial evidence, and persisted assessments. All controller phases receive that operational evidence; diagnostic logs and run usage are excluded.

The meta-queen asks for plain-text 0/1/2 ratings; she does not compute or enforce an aggregate threshold. All three current criterion weights are 1. The base queen separately evaluates each task criterion with the Decisions expected zero-based rating; consistency and testability each require 1.5/2 for criteria_met. Weights do not combine these into an aggregate acceptance score. The base queen still decides RETURN or CONTINUE and may return unmet criteria honestly.

A fresh reference assessment is a characterization, not a comparison or improvement claim. Earlier cross-experiment comparison text is not imported without its corresponding parent evidence. Prior assessments produced within this campaign remain available for all subsequent choices.
