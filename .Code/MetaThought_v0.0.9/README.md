# MetaThought v0.0.9

An early observation runner for multiple organic inquiry teams, starting from the frozen Queen handoff.

Run-InquiryExperiment.ps1 prepares a fresh folder, preserves the original handoff/role, applies the authorized "two drones" to "multiple drones" phrase change to working copies, and runs the new inquire command. No Queen call is replayed. Each drone takes Contribute and Expand turns and may propose, join, contribute, connect or pass. PROPOSE/JOIN are explicit commitments; a team is an inquiry with multiple committed drones. Membership can overlap. No founder privilege or assigned role.

All prompts, schema, model routing and starting population are in config.json. Calls use the existing Meta client and Spark Contributor. The working state has hive_inquiries, hive_inquiry_members and hive_inquiry_work. Existing logged SQL access and separate debug.db diagnostics are reused; agents never read diagnostics. Exact provider replies remain preserved. Replies use three explicit H1 sections and actual local relevance/inquiry_value scores in [0,1]. These do not assess final SIDS hypotheses.

This experiment stops between calls when multiple inquiries each have multiple committed contributors. The stop is a developer observation endpoint, absent from model prompts. It preserves the Queen request as paused/unfinished. A full idle cycle or actual failure also returns without invented completion. No call/token/time budget or retry loop.

Run-InquiryTests.ps1 runs production code and only v009 fixtures with HTTP blocked. Earlier fixtures are preserved separately and excluded. Build.ps1 -Step build compiles the production executable with Go 1.27 and VCS stamping disabled. No new dependency.

Limits: this is a shared-board sequential prototype, not evidence of novel team topology or scientific validity. It has no web research or medical-trial tool. The six-drone pool is an initial experimental population, not an expandable drone factory. The early runner has no full Queen convergence loop or resume path; those are later work. Existing legacy commands remain for historical compatibility and do not gain these inquiry actions automatically.
