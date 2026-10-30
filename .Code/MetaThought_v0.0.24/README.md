# MetaThought v0.0.17

Queen-guided resource health with voluntary newborn choice. This version replaces the five v0.0.16 prompt/routing variants. Their source variants, SQLite runs, original prompts and analysis remain historical evidence; no old run is resumed or reset.

## Runtime behavior

- Keep the pre-experiment v0.0.15 Contribute/Expand chromosomes and prompts. Add only the newborn-menu instruction and the separate scored Queen resource-review prompts needed for this mechanism. Pair-only routing, rotating membership, recruitment-wording variants, extra action-distinction wording and smallest-first menu ordering are absent.
- Every uncommitted newborn sees compact inquiry cards under its own request and chooses freely. Existing teams persist and overlap.
- The Queen reviews resource distribution at the next available Queen slot after `workers.resource_review_every` additional successful SPAWN births. Default: 20; set 10 to review more often, or 0 to disable this review. This is review cadence, not a population or work limit. Initial seed drones do not count as newborn births.
- The Queen may SELECT one or several non-root inquiry IDs or CLEAR her selection. She judges overall resource health, allowing strong attractors larger swarms. There is no sigma threshold or equal-size goal. This call includes the accepted handoff, current revision, per-inquiry membership/activity facts and one latest original work item per inquiry. Debug logs never enter model context.
- At birth, each drone receives one random inclusion/omission cohort flag with probability `workers.newborn_omit_probability` (default one-third). For the omission cohort, all currently selected inquiry cards are absent from the initial choice menu. The flag persists across READ calls and restart; this is not an exact one-in-three quota.
- Explicit DISCOVER can reveal the full menu again. Explicit READ and JOIN remain allowed for same-request inquiries, and existing memberships and ongoing-work context are unchanged. The mechanism changes visibility, not permission to join.
- Resource selection persists until the next accepted resource review. No extra Queen call occurs per newborn. One birth-count checkpoint and one current selection row support cadence/restart; births are counted from existing saved SPAWN work, not an incremented accounting ledger.
- SELECT/CLEAR replies are plain text with the established two numeric local-score footer. Invalid replies and provider failures stay visible through existing failure/recovery handling. Queen resource scores assess her decision, not the research hypotheses.

## Configuration and storage

All settings, prompts and SQL schema are in `config.json`. `hive_worker_resources` stores the latest review; `hive_worker_queue.omit_oversized` stores a newborn cohort: NULL identifies an original seed, while every birth saves 0 or 1, including births focused on the entry point. The successful-birth query has a partial index. Normal SQL and model diagnostics remain profuse. No new third-party dependency is used. Source, executable and configuration remain flat within this version folder.

The inherited configured models are Spark Contributor for drones and Queen, matching the recent experiments. Four concurrent execution slots retain the provider adjustment from those runs; slots are distinct from drones.

## Build and local verification

Use PowerShell 7 with the existing Go installation:

```powershell
./Build.ps1 -Step build
./Run-WorkerTests.ps1 -EvidenceFolder <absolute-new-evidence-folder>
```

The focused runner compiles current production code with the v010, v015 and v017 behavioral fixtures and the shared HTTP-blocking TestMain. It excludes incompatible historical test harnesses. The inherited `Build.ps1 -Step test` is not the supported focused test route.

## Prepare a fresh experiment

Use `Run-WorkerExperiment.ps1 -Stage Prepare` with a fresh `.Experiments/MetaThought_v0.0.17a - Queen resource health` folder and explicit `-HandoffPath` / `-HandoffRolePath` from the currently accepted Queen handoff. Preparation makes byte-for-byte handoff copies and does not call a model. `-Stage Run` starts a separately authorized live run. Never resume old v0.0.16 databases with the new queue schema.

`Initialize-Version.ps1` records the reusable source-copy process and excludes old runtime data and five-way launch manifests. `version-origin.json` records source provenance.

## Dashboard presentation update — October 29
The dashboard now keeps the full Markdown handoff collapsed with the Objective visible, labels parent scores INTERIM, uses short inquiry display names, shows source-labeled Queen local scores, and collapses the Queen and bottom drone pool. Auto-refresh defaults off. Protein ties use recorded CONNECT counts and shared members; spherical shading, two-line names, rotation buttons and zoom through1000% are supported. These are human-view transformations; raw operational records and model prompts are unchanged. Missing parent assessments stay unassessed.

For an updated dashboard build in a stopped saved run, use Resume-WorkerExperiment.ps1 -Stage Update -BinaryOnly -RunFolder <absolute-folder>; this preserves configuration bytes and changes only the executable. Then use the existing Publish-Dashboard.ps1 with the run config. -Stage Run continues a saved experiment without an update. A normal Update refreshes its selected fields (including prompts/dashboard), but does not overwrite resource_review_every; earlier session wording suggesting it resets that cadence was incorrect.
