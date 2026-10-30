# Protein mission colors

User request October 28, 2026: shade molecule drones by type/class/mission and make cross-team drones unmistakable.

The existing dashboard has recorded inquiry memberships and inquiry questions, but no persisted drone type/class. Use mission/inquiry identity as the color category; do not invent semantic roles or make model calls. Give each inquiry a stable distinguishable muted/cool hue, tied to its ID, with matching atom gradients and a visible inquiry legend. Keep the exact inquiry question available in the legend title or details.

Reserve one vivid contrasting color for any drone with multiple distinct saved inquiry memberships; use that same color for every occurrence plus a visible outer ring so overlap is recognizable without relying only on color. Label this shared across inquiries, including forming inquiries, rather than claiming two formed teams. Shared styling overrides mission hue. Selection adds a white outline and retains category color. Existing Structure legend stays accurate when switching views. Do not change 3D geometry, rotation, selection, underlying membership, prompts, schema, or original evidence. No extra libraries or runtime accounting.

Acceptance: inspect the actual rendered browser DOM and screenshot. Different non-shared inquiries have distinct fills matching their legend. Shared Drone 1 in inquiries 3 and 7 has the same standout fill and ring, including when selected; its details retain exact memberships. Rotate once to confirm styles remain attached to IDs. Existing focused Dashboard/Protein tests, build and Publish-Dashboard.ps1 must pass after the edit. Save receipts and new screenshot; update design decisions/session notes.
