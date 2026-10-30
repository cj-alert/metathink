## FOLDER STRUCTURE and File Locations

.Discussion = All discussions, decisions go in this folder. This folder must be git ignored.

.SessionNotes = All session notes go in this folder. This folder must be git ignored.

.Analysis = This folder is where all analysis of experiments goes.

.Code = This folder is for all code. All code must be in a versioned subfolder. ie. MetaThought_v0.0.1

.Experiments = This folder holds all experimental runs, and should be in a subfolder with the version number followed by a letter, followed by a short description of the objective. ie. `MetaThought_v0.0.1a - Test if drones can read`

All .temp* folders are to be git ignored.

## CANONICAL SCORING CONTRACT — mandatory

Founder direction, October 27, 2026: **EVERY PROMPT MUST HAVE EXPLICIT SCORING ELEMENTS. EVERY ELEMENT MUST HAVE ITS OWN ACTUAL NUMERIC SCORE FROM 0 TO 1.**

Read and follow [.Discussion/10-27_MetaThought_Scoring_Contract.md](.Discussion/10-27_MetaThought_Scoring_Contract.md) before designing or changing prompts, contracts, scoring, evaluation, collapse, or queen/meta-queen behavior. This is the authoritative scoring specification; conflicting older proposals or current code do not weaken it.

The rule applies throughout MetaThought, including drones, Queen, Meta-Queen and Meta-Meta-Queen. Target, actual score, optional weight and confidence are distinct. Missing scores cannot be treated as complete assessments; scores that miss targets must be reported honestly. Preserve the simplicity/no-unnecessary-accounting rule. Do not claim implementation conformance until all relevant prompt paths supply the required per-element scores.

Scoring ownership (D82): the Queen distills and confirms the governing objectives and scoring contract with the user before exploration. Drones/teams provide actual scores and evidence. The Queen guides inquiry and judges sufficient critical mass for collapse; do not substitute Queen-generated grades or a fixed score/count formula. This division repeats at meta levels.


## CANONICAL QUEEN PROCESS — mandatory

Founder direction, October 27, 2026: the Queen has exactly three core responsibilities: **structure and delegate; maintain alignment; decide when to begin collapse and return conclusions.**

Read and follow [.Discussion/10-27_MetaThought_Queen_Process.md](.Discussion/10-27_MetaThought_Queen_Process.md) before designing or changing Queen prompts, intake, delegation, guidance, collapse, or meta-level equivalents. User confirmation belongs inside structuring; evidence-supported final reporting completes collapse. Drones/teams own exploration, testing, scores and team organization.

A fresh inquiry retains the Queen role and current-phase instructions while excluding prior inquiry data unless requested. Preserve the exact user request and explicit criteria/thresholds. Do not treat a bare question that the model answers itself as successful Queen orchestration. Canonical documentation is not runtime conformance proof.
