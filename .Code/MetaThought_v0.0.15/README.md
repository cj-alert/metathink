# MetaThought v0.0.15

Isolated test of a simpler scored action reply: ACTION: command, plain-text answer, then a --- footer and two unlabeled numeric lines in fixed order: relevance followed by inquiry_value. The parser tolerates observed cosmetic labels and optional separator. Legacy H1 replies remain supported. Required numeric scores, action syntax, membership rules and raw reply preservation remain enforced.

Based on v0.0.14 with initial-arrival rotation unchanged. Both drone and Queen model routes use muse-spark-1.3-contributor. The live test continues a stopped copy of v0.0.14a after preserving its failed Queen reply; no historical output is repaired. Final-return and parent-assessment protocols are unchanged.

Run-WorkerTests.ps1 runs selected offline fixtures with HTTP blocked. Resume-DistributionObservation.ps1 resumes saved work and requests native pause after a short external observation period.
