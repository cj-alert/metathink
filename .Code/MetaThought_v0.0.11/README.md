# MetaThought v0.0.11

This version tests one atomic Expand chromosome change: offer an idea, question, or connection that could change another drone thinking. The Go engine differs from v0.0.10 only in its version identifier; dependencies are unchanged.

See BUILD_PLAN.md for the repeatable checkpoint preparation, short observation and pause process. Run-DistributionObservation.ps1 wraps the existing launcher with an external pause timer; verify actual stopped state and released claims afterward. Analyze-TeamDistribution.py reads SQLite without mutation and compares matched early prefixes.

Current experiment: .Experiments/MetaThought_v0.0.11a - Expand influence distribution test. It is paused; no implicit continuation is authorized. See ../../.Analysis/10-28_MetaThought_v0.0.11a_Distribution_Comparison.md for results and limits.

Build: pwsh -NoProfile -File ./Build.ps1 -Step build
All prompts/settings/schema remain in config.json; runtime files remain flat inside each experiment folder.
