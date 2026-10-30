# MetaThought v0.0.2

A small sequential Go hive for experiments in shared thinking. The application uses Meta, with plain-text model replies behind the full Decisions request/answer interface. All runtime files stay flat in the selected run folder.

## What works

- Fit-based discovery of persistent teams; at least two explicit commitments per request. Membership can overlap and the founder has no permanent authority.
- Atomic Contribute and Expand calls, shared operational contributions, directed information exchanges, and peer invitations.
- Wonder contract drafting, criterion scoring, guidance, and queen-controlled collapse. Each fresh request adds an obligation.
- Explicit continuation of an unfinished request. Completed contributions are preserved; continuation starts new atomic work after the last saved tick without replaying completed contributions.
- Full predicate/choice/score/refusal shapes, typed boolean/string choices, distributions/confidence, weighted zero-based scores, text/image/JSON/conversation/tool evidence.
- Separate state.db and debug.db, indexed hive/meta-queen tables and logs, every application SQL operation summarized, complete provider requests/replies, parsing outcomes, timings and returned usage. Actors never read debug.db. Log-sink setup/inserts are excluded from recursive instrumentation.
- Fresh Goja runtime for optional calculations. A contribution beginning with exactly `# JAVASCRIPT` on its own line runs its following script; the result becomes an operational contribution. The CLI also supports eval.

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

Choose a fresh `.Experiments/MetaThought_v0.0.2<letter> - <objective>` folder. The script preserves earlier outputs and copies the executable and initialization configuration.

```powershell
pwsh -NoProfile -File .\Run-Experiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.2d - First inquiry" -Stage init
pwsh -NoProfile -File .\Run-Experiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.2d - First inquiry" -Stage decisions
pwsh -NoProfile -File .\Run-Experiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.2d - First inquiry" -Stage plan
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

Selecting `-contract` is explicit caller confirmation. `resume` continues the same obligation; `run -parent` creates a new one and retains the old. `resume` selects a unique output filename. Source/config must match the database version; v0.0.2 initializes its own schema and does not include migration between experimental schema revisions. Earlier failed development snapshots remain historical.

## Decisions compatibility boundaries

The public request/answer types follow the official Decisions contract, including optional names, per-question refusals, choice types, nested evidence, and usage. Meta estimates are self-reported, not assumed calibrated or equivalent to native Decisions. The adapter renders the same question content and uses explicit plain-text sections.

Meta supports forwarding prompt_cache_key and safety_identifier. Native Decisions cache mode/TTL and explicit breakpoints have no established faithful Meta mapping and return unsupported_feature; they are never silently discarded. Unreported usage details serialize as null, rather than inventing zeros. These are explicit provider differences, not full native wire/behavior parity. One logical request may require one atomic Meta call per question.

## Current scope

This version adds a thin meta-queen propose/run/compare cycle around the existing hive. Each invocation performs one atomic prompt-variation experiment and records an assessment and next hypothesis. It does not yet implement population-level genetic selection or automatic promotion. A/B/mixed population comparisons remain later work. No external search or discovered-information invalidation system is implemented. The sensor problem is a technical smoke case; it cannot establish novelty or validate SIDS hypotheses.

Goja exposes no filesystem, network, or process APIs and supports caller cancellation. It is not a heap-memory sandbox. The application is sequential; do not run simultaneous hive workers against one run folder. No local quota is imposed to substitute for queen judgment.

## Evidence

Automated tests cover real SQLite persistence/isolation/logging, provider and Decisions parsing, typed values, cancellable JS, multiple-drone interaction, fresh obligations, failure preservation, and continuation. Saved live experiment folders retain their exact executable/config and all outcomes. Read the project .Analysis report for measured live status.

## Meta-queen experiment cycle

Use a completed saved baseline and a fresh run folder. The meta-queen reads only the baseline's operational state, proposes one replacement instruction, runs the unchanged contract through the existing hive, and compares both attributed trajectories. The baseline is opened read-only and its model calls are not repeated.

```powershell
pwsh -NoProfile -File .\Run-MetaExperiment.ps1 -RunFolder "C:\path\.Experiments\MetaThought_v0.0.2a - Prompt variation" -BaselineFolder "C:\path\.Experiments\MetaThought_v0.0.1d - No quota hive" -BaselineRequestID 1
```

The direct command is `MetaThought.exe experiment -config CONFIG -dir NEW_FOLDER -baseline BASELINE_FOLDER -baseline-request 1`. Optional `-prior ASSESSMENT_FILE` supplies a previous comparison as operational feedback; the wrapper names it `-PriorAssessment`. Repeating the cycle with feedback is the initial meta-queen loop. The selected baseline may remain the original or a prior candidate; no candidate is silently promoted.

`meta_queen` in config selects objective, problem type, zero-based chromosome index, behavior (contribute/expand), and explicit comparative criteria. `mq_propose` asks for one plain instruction. `mq_compare` asks for plain-text ratings, attributed evidence, uncertainty, and the next hypothesis. Ratings are observations, not runtime gates. Base hive prompts and task criteria remain those saved with the baseline. The meta-queen uses controller model/connection settings. The candidate preserves the baseline model, reasoning, and endpoint settings; only its credential source, schema/version, controller metadata, and selected instruction are refreshed.

Saved files stay flat: `mq_config.json` (controller), `config.json` (actual variant), original contract, candidate result, exact `comparison.md`, state/debug DBs, executable, stdout/stderr. `mq_experiments` retains the experiment and failure state; `mq_log` retains meta-queen calls and data access. Drones/queens/meta-queen never receive developer debug logs.

A single run can reveal behavior, not prove causality or general improvement. The evaluator is model-based and may be wrong; inspect its cited operational evidence. Provider failures are preserved, never silently retried.
