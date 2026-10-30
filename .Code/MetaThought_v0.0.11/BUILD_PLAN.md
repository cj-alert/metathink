# v0.0.11: Expand influence distribution experiment

Authorized October28,2026: new version/folder, fresh test from Queen checkpoint, run only long enough to compare team distribution.

Single behavioral change: replace chromosome A Expand with "Offer an idea, question, or connection that could change another drone's thinking." Preserve all other prompts, chromosome B, scheduler, discovery, worker count, initial population, criteria and Meta models. Do not implement the other proposed changes in this experiment.

Preparation: copy flat source/config/schema and reusable scripts from v0.0.10 into this new version, excluding executables, databases, logs and historical backup requests. Update version identity, compile with existing Go1.27 and approved dependencies. Prove the config differs only by version and the selected Expand instruction; do not add accounting or hashes to the hive.

Use existing Run-WorkerExperiment.ps1 Prepare with frozen v0.0.8f Queen checkpoint plus the exact revised Queen handoff/role used by fresh v0.0.10o (v0.0.10p reply.md/role.txt). Reuse these saved outputs; no new Queen intake call and no inherited drone state.

Observation: compare early membership and substantive-action distributions with fresh o at matched early work boundaries. Inspect saved state approximately once per minute. Pause natively as soon as there is enough actual formation to describe concentration/branching, aiming for around200saved actions rather than thousands. In any case request pause within5minutes of launch; this is an operator experiment boundary, not a model prompt, production quota or claim of convergence. Drain in-flight calls and preserve their answers; no automatic restart on failure. If sample is too small or provider errors prevent comparison, report inconclusive.

Report committed counts and largest-team share, inquiry creation, spawned participants, cross-inquiry participation and representative original work. Counts alone do not prove intellectual influence or better scientific answers. Publish the saved dashboard through the existing process and finish with the test paused. Existing l/n/o remain paused and untouched.
