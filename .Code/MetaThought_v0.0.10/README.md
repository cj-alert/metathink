# MetaThought v0.0.10

Concurrent local workers advance persistent drones from the frozen Queen handoff. `workers.count` is configurable and starts at **10**. A worker is an execution slot; it is not a drone, a team or a leader. The starting drone population is configured separately by `hive.drone_count` and can grow through drone actions.

## Thinking and membership

Every drone uses Contribute and Expand and the same action interface: `PROPOSE`, `JOIN`, `CONTRIBUTE`, `CONNECT`, `PASS`, `SPAWN`, `DISCOVER`, and `READ`. `PROPOSE` and `JOIN` explicitly commit the acting drone. Multiple committed drones form a team; memberships persist and may overlap. Proposing an inquiry grants no authority.

`SPAWN <inquiry_id>` invites a new independent participant. It creates no automatic membership; the participant decides whether to commit. Every turn automatically offers one other inquiry through the existing discovery cursor; the initial unowned inquiry is an entry point for joining or opening a focused question. `DISCOVER` advances browsing without changing membership. `READ <work_id>` requests a saved work artifact for a later turn. A `CONNECT` offer does not establish that another inquiry used it; later work must show that influence.

Replies use `# ACTION`, `# YOUR ANSWER`, and `# YOUR SCORE`, with actual local `relevance` and `inquiry_value` scores from 0 to 1. Their basis and uncertainty belong in the answer. Local scores do not replace the accepted research criteria or establish novelty.

One logical Queen monitors each request when unseen operational work exists. Her fixed role and accepted handoff remain outside mutation. `NOOP`, `GUIDE`, and `BEGIN_COLLAPSE` maintain alignment or begin convergence; they do not independently solve the problem or replace drone scores. Her local decision scores are self-assessments, not independent drone evaluations. After collapse begins, workers drain and save their in-flight responses. Each formed team produces a report from its latest attributed contributions and connections; a different committed contributor assesses every exact handoff criterion. The Queen selects reports and returns supported conclusions, scores, uncertainty and unmet findings. Reports, assessments and the exact final reply persist in existing operational tables. A returned result does not establish research success.

`BEGIN_COLLAPSE` requests those team reports and assessments; it does not require every target to be met or every saved record to have been reviewed. Completion keeps the original handoff intact. For the accepted legacy handoff, criterion names and exact comparisons are read from its explicit target lines, while the original text supplies definitions and unanswered questions. No configuration rubric replaces them. A missing or malformed assessment remains incomplete. A Queen `MET` return is rejected when a selected report's drone scores miss a required target; `UNMET` and `UNCERTAIN` can be honest completed returns.

## Fresh experiment

Build the v0.0.10 executable before preparing a run:

```powershell
.\Build.ps1 -Step build
.\Run-WorkerExperiment.ps1 -RunFolder 'C:\path\to\fresh-run' -Stage Prepare
.\Run-WorkerExperiment.ps1 -RunFolder 'C:\path\to\fresh-run' -Stage Run
```

`-Stage PrepareAndRun` combines those last two steps. `-CheckpointFolder` optionally selects a frozen checkpoint during preparation; the default is `.Experiments/MetaThought_v0.0.8f - Frozen Queen handoff` under this project.

Preparation copies this version's configuration and executable into a new flat runtime folder. It copies the frozen question, accepted intake and intake-role file unchanged, preserves original handoff/role files separately, and applies only the authorized case-insensitive phrase replacement `two drones` → `multiple drones` to their working copies. It copies no prior database or diagnostic log. Preparation makes no provider call and does not regenerate Queen intake or handoff.

The current default is `workers.observe: "none"` for a complete run. Early observation experiments can use `workers.observe: "growth"`. Its developer observer pauses dispatch after multiple inquiries have multiple committed contributors and a spawned participant has contributed to one of those teams. Calls already in flight are allowed to finish. The request remains unfinished. This observation condition is absent from model prompts and is not a research score, exploration quota or general collapse rule. `workers.observe: "none"` disables that observation boundary. There are no call, token or time budgets.

The launcher invokes `workers -handoff <saved-file> -confirm`. It preserves `workers.stdout.txt`, `workers.stderr.txt`, the exit code and a JSON receipt. The executable saves a new `worker-result-<timestamp>.json`. Configuration, SQLite working state and diagnostic state remain in the same run folder. Existing attempts are never overwritten or automatically replayed.

## Pause dispatch

`worker-pause -dir RUN` requests an operational observation pause. New calls stop and already-running calls finish and save. A request already converging remains converging. This is an operator control, not a call/time/token quota.

## Continue or recover saved work

Use the saved executable and directory to continue the saved request and configuration:

```powershell
& 'C:\path\to\run\MetaThought.exe' work -dir 'C:\path\to\run'
```

This is continuation, not a new `workers` start. Each continuation writes a new timestamped result. A request for which the Queen has begun convergence resumes completion using saved reports and assessments. A returned request reuses its saved answer without model calls.

If an interrupted process left an owner claimed, first establish that the owner has stopped and review the saved provider call and operational state. An in-flight response may have an uncertain outcome; recovery does not establish exactly-once execution. Release only that explicit owner, then use `work`:

```powershell
& 'C:\path\to\run\MetaThought.exe' worker-recover -dir 'C:\path\to\run' -owner 'EXACT-SAVED-OWNER' -owner-stopped
```

There is no automatic stale-owner timeout or blind replay. Preserve failed outputs and diagnostics for review.

To update a stopped run after building an authorized process correction, use the continuation launcher:

```powershell
.\Resume-WorkerExperiment.ps1 -RunFolder 'C:\path\to\run' -Stage Update
.\Resume-WorkerExperiment.ps1 -RunFolder 'C:\path\to\run' -Stage Run
```

`UpdateAndRun` combines those stages. The default is `Update`, which makes no model calls. The launcher checks that the exact run executable is stopped and that the latest saved worker result permits continuation. It preserves timestamped copies of the existing executable and config in the same flat folder. It updates only `prompts`, `dashboard`, and `workers.observe` from the code folder, retaining the run's other settings and credentials. `-SourceFolder` selects another explicitly built source folder. A known failed attempt requires `-AllowFailed`; this does not release uncertain claims or replace the explicit recovery command.

Continuation invokes `work` and saves fresh `workers-continue-<timestamp>.stdout.txt`, `.stderr.txt`, `.exitcode.txt`, and `.receipt.json` files, alongside the new `worker-result-<timestamp>.json`. Earlier receipts, state, original handoff and generated answers remain preserved.

## Live dashboard

When `dashboard.enabled` is true, `workers` and `work` write the configured HTML file in the run folder. The current defaults are `dashboard.html` and a five-second `dashboard.refresh_seconds` interval. Open that local file to inspect the saved state. The worker process refreshes the file during work and writes a final snapshot when it stops.

A standalone viewer can generate one snapshot or keep refreshing an existing run, including one executing an older saved binary:

```powershell
.\MetaThought.exe dashboard -dir 'C:\path\to\run'
.\MetaThought.exe dashboard -dir 'C:\path\to\run' -watch
```

`dashboard` opens the existing operational database read-only, makes no model calls, and changes no request state. It writes the HTML and diagnostic access/update records. `-watch` stops after a paused, failed or returned state, or Ctrl+C. The browser's auto-refresh button controls page reloads only; it does not pause the hive. Selection, detail tab and layout preference persist locally in the browser when local storage is available. Check the displayed snapshot time to distinguish fresh state from a stopped writer.

**Structure** groups saved inquiries and their committed members, with directed `CONNECT` offers between inquiries. The Queen-to-inquiry lines show the governing request relationship, not a fixed leadership hierarchy. A shared drone shown in more than one cluster is one persistent actor with overlapping memberships. Forming inquiries and uncommitted drones stay visible.

**Protein** rearranges the same saved relationships into a compact fold-like view. Shared memberships and connection offers draw inquiry clusters toward one another. Distance and shape are illustrative; they are not measured similarity, a discovered biological structure or proof of intellectual influence. Select an inquiry, drone, Queen or connection to inspect its original evidence.

The detail tabs expose the exact objective, latest saved work, team reports, parent criterion scores, evidence, uncertainty and saved return. Local action scores remain distinct from parent outcome scores. A connection is an offered influence; demonstrated uptake requires later attributed work. Worker slots show saved claims and phases, not independently verified process liveness. The phase display marks `Returned` only from saved returned state; pending reports are not displayed as completed results.

## Local verification and evidence boundary

```powershell
.\Run-WorkerTests.ps1 -EvidenceFolder 'C:\path\to\local-test-evidence'
.\Run-WorkerTests.ps1 -EvidenceFolder 'C:\path\to\focused-check' -TestPattern '^TestV010(Dashboard|EndToEnd)'
```

This compiles production source plus `v009_inquiry_test.go` for its shared fixtures and HTTP-blocking `TestMain`, and the `v010_*_test.go` fixtures. It runs only `^TestV010`. Earlier test groups are not run. The launcher selects the local Go executable explicitly, disables module downloads and VCS stamping, and preserves Go JSON output, stderr and a receipt. The shared guard reports HTTP attempts and fails the local checks if any occur. No test or live acceptance result is implied by this documentation.

The storage backend is **local SQLite only**. Operational inquiry/work and queue records are separate from profuse developer diagnostics, including provider replies and SQL access summaries. Agents never receive diagnostic tables or files. Spark Contributor performs drone calls; Wonder performs Queen monitoring through the existing Meta transport. No new dependencies were added for the worker approach.

This version supplies mechanisms for concurrent inquiry, discovery and participant growth. It does not establish novel team structures, scientific validity or verified medical hypotheses, and includes no web-research or medical-trial tool. No 100,000-worker or distributed-scale capability has been verified. Historical commands remain available for their own workflows; use `workers` and `work` for this worker process.

## Instance pages on the existing webserver

Current configuration publishes to the existing CondoCodex IIS root at C:/ProgramData/FounderOS/CodexProjectDeck/web (HTTP port39119). This path is machine-local and editable in config.json; no server is created by MetaThought.

- dashboard.file: {instance}.html
- dashboard.instance_name: optional; blank uses the exact run-folder basename.
- dashboard.publish_directory: existing webroot; blank disables web publication.

Every program refresh writes the same HTML locally and to that directory. The name appears in the page and browser tab. Different instance folders retain distinct names, including the version letter; use instance_name if a custom identity is needed. Only generated HTML is published.

For an existing saved run, invoke Publish-Dashboard.ps1 -RunFolder RUN -BaseUrl http://127.0.0.1:39119/. It calls the built program's read-only dashboard command with the current source configuration, verifies the HTTP page and records a timestamped receipt in RUN. No Meta calls or operational state changes occur. The native dashboard -dir RUN -config CONFIG -watch command can refresh an older running instance from the same configuration.

## Rotatable molecular view

Protein view uses three-dimensional coordinates, perspective and depth ordering with shaded atoms. Drag with a mouse or touch to rotate; use the wheel or visible controls to zoom, and Reset view to restore the default. Keyboard controls are shown beside the view. Click an atom, team or saved connection to inspect its evidence. Viewing angle and zoom are instance-specific preferences preserved across normal page refresh; refresh waits while a rotation gesture is active. Structure remains available. Geometry and colors visualize relationships and do not assert a biological structure or research validity.
