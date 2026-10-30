# MetaThought_v0.0.8

A raw question goes to the Queen, who proposes objectives, scoring elements, targets and an evaluation approach. The user reviews her exact saved proposal before drones begin.

## Planning

Use `Run-Planning.ps1` to create a fresh flat planning folder from this version, forward a question file to the real Meta Queen, and save the complete reply unchanged. `MetaThought.exe plan -question question.txt -out proposal.md` performs the same operation from an initialized configuration folder. `meta-plan` uses the same question-driven intake for a method inquiry.

To revise, pass the earlier proposal with `-draft` and exact user feedback with `-feedback`, using a new `-out` filename. Prior proposals are not overwritten. The front end does not rewrite Queen replies.

## Confirmation

After explicit user approval, `MetaThought.exe run -contract proposal.md -confirm` uses the selected saved contract. Drafting alone creates diagnostics and the proposal, not a hive or team. Meta inquiry execution still requires its own confirmed contract and a fresh execution folder; pass the saved proposal path from the planning folder. No approval ledger, hash or matching machinery is added.

## Scope

All v7 prompt strings, chromosome instructions, Decisions prompts, models, schemas and dependencies are preserved. The new intake instruction is the Founder’s exact wording. Legacy draft presets do not enter question planning; problem_type metadata is neutral. Debug logs remain developer-only.

This change does not prove novelty, scientific validity, organic teaming or reliable method improvement. Auxiliary per-prompt scoring and external research verification remain incomplete. Existing experiments are retained unchanged.

## Validation

Build/version and six focused local test groups (13 subcases) passed. The checks used injected replies and real SQLite with zero HTTP attempts; they do not establish live model quality. See ../../.Analysis/10-27_MetaThought_v0.0.8_Queen_Planning.md for evidence. No drone work starts before the actual generated proposal is confirmed.
