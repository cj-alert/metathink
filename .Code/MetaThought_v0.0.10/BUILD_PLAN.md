# v0.0.10: organic growth with ten workers

Authorized October 28, 2026. Fix fixed population and eager in-memory execution. Work is local to one computer and SQLite; multiple-server work is deferred by the Founder.

## Repeatable process and acceptance

1. Preserve v0.0.9 and its runs. Add this version with config-owned worker count 10 and no team-size, team-count, drone-population or call/token quota.
2. Keep drones in SQLite. Workers atomically claim one drone, perform its saved Contribute/Expand phase outside SQL transactions, and commit action plus next phase together. New SPAWN invitations create independent participants at the fair queue tail. They must explicitly join before counting as team members.
3. Load one actor and a focused exchange, not every drone or historical board. Keep original records readable. Compact prompts develop one contribution without repeating the handoff. Discovery remains available across inquiries.
4. Serialize one logical Queen per request. Each worker checks for unreviewed operational work; no new work or an active Queen means no extra Queen call. Review only the captured evidence; new arrivals remain pending. NOOP changes no drone objective. Guidance preserves the fixed contract and does not prescribe roles. BEGIN_COLLAPSE requests the convergence phase without claiming a solved research result.
5. Preserve incomplete claims and completed phases on failure. Never expire a live provider call or replay an unknown outcome automatically. Explicit recovery releases only a chosen stopped worker after reviewing preserved evidence.
6. Local tests use injected replies, real SQLite, ten concurrent workers and a larger population/team fixture. Prove exclusive claims, overlap of model calls, atomic progress, spawning/fairness, relevant context, Queen no-op/checkpoints and preserved failure. Build only after these checks.
7. Run fresh branches from the preserved handoff with original outputs intact. First observe spawned drones becoming actual contributors and multiple teams, then review real formation and context sizes. Subsequent experiments test sustained exchange and larger organic structures. Do not claim dozens of useful teams from a synthetic capacity test or promote novelty without evidence.

Configuration is the owner of prompts/schema/settings. Existing dependencies only. All application SQL reads/writes and provider calls remain profusely logged; diagnostic records never enter agent context. Necessary ownership and phase state prevent duplicate concurrent turns; they are not scores, budgets or exploration gates.

## Live observation control

worker-pause -dir RUN requests an operational pause and lets already-running calls finish and save. It changes no generated answer and creates no model call. Use this when the observed behavior itself answers or invalidates the current experiment question, including repeated distinct questions staying in one broad inquiry. Preserve that attempt, repair the generating prompt/process, and use a fresh branch for the next comparison.

## Branching comparison after v10c

The completed v10c run created 19 additional drones but no additional inquiry. Preserve its complete record. Test one prompt change only: explicitly map a distinct question requiring its own investigation to PROPOSE, while CONTRIBUTE develops the current inquiry. Keep root wording, Queen instructions, chromosome pairs, worker count and handoff unchanged. Run a fresh v10d branch through Run-WorkerExperiment.ps1. Acceptance remains actual multiple teams plus a spawned contributor; no prescribed questions, roles, population or team targets enter model prompts. Analyze unchanged outputs through the existing read-only analyzer.
