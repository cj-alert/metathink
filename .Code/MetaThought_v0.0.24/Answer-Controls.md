# Native answer controls

Use an existing runtime with the current full v23configuration/schema. These commands do not create a fresh research question.

MetaThought.exe collapse-now -dir "<runtime folder>"
MetaThought.exe finish -dir "<runtime folder>"

Add -request-id <id> to select an obligation, -config <file> for an explicit configuration, or -wait=false to queue without waiting. Collapse-now leaves thinkers active. Finish drains ongoing work and closes by user choice with the best available findings, including unmet/unknown scores. It does not claim success.

workers.continuous=true keeps requests open untilFinish;false allows the existing automatic qualifying completion. Runtime full updates include new prompt-context/schema settings; binary-only updates intentionally preserve existing settings.

Each answer is retained in hive_request_answers and exported as request-answer-<id>.json. The dashboard displays the latest interim/final answer. There is no new control HTTP server or dashboard action-button endpoint.

The saved v23a staging copy contains an already-returned request; no work is running there. A closed request returns its saved result and is not reopened by either command. Start a new or explicitly revised/continued request only when the user asks.
