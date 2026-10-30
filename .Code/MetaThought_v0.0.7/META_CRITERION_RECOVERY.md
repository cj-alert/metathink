# Method-assessment criterion recovery

October 27, 2026. Applies to the confirmed v7 live sequence after v7c failed at reference assessment. This is one evidence-backed generating-prompt correction, not acceptance of its rejected output.

## Evidence

The saved request contained correct method criteria in top-level scoring_elements and meta_contract, alongside the base task contract and evidence. Its generic developer prompt said confirmed contract without identifying which input field governs the reply. Spark returned # consistency and # testability; ParseAssessment rejected unknown criterion consistency. Four provider calls succeeded, one assessment parse failed, and no method assessment was accepted. The failed campaign and original prompt/config/output remain untouched in v7c and its saved failure transcript.

## Correction and verification

1. Back up source config.json. Change only prompts.assess to designate top-level scoring_elements as the sole output rubric and require its exact names. Preserve existing score/evidence/uncertainty format, targets, ownership and parser. No model substitution, code change, quotas or coercion of the rejected scores.
2. Both base and meta paths already supply this field. Use plain instruction text without a new template dependency; the base renderer lacks a Contract template binding. Read back the configuration and check that only the intended prompt differs. Restore the unneeded test edit made before this simpler route was selected; do not rerun passed Go checks for a plain prompt edit.
3. Do not edit the failed campaign's saved configuration or resume it with changed inputs. Start one fresh v7d corrected method campaign with the unchanged confirmed contracts and v7b saved baseline. Its own snapshot preserves the new prompt. Three new commitments belong to that new inquiry; no completed baseline is replayed.
4. The corrected live response is the direct behavioral check. Preserve/report any next failure without another speculative correction or blind retry. Let a successful campaign run to its own return, without a trial/turn quota. Keep costs of v7c separate and include them once in the whole-session total.
5. Use the existing read-only reports. Describe the prompt clarification as a harness correction, not evolved behavior or proof of generalized improvement. Candidate child assessments use the clarified fixed protocol; the base reference retains its original record and criteria.
