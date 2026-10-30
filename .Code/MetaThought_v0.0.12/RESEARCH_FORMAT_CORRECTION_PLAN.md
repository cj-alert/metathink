# Research response format correction

The preserved live k run returned real search results, but the action parser rejected Markdown subheadings in the answer. Some replies also added descriptions to the command line. Keep those originals and their recorded rejection status unchanged.

1. Back up `inquiry_action.go` with the canonical verified backup helper. The current-turn `config.json` backup already exists as v004.
2. Accept Markdown H2 through H6 only inside `YOUR ANSWER`. Keep exact H1 section names, duplicate-section checks, command arity/IDs, and both finite numeric scores unchanged.
3. Clarify `worker_research`: ACTION is exactly one line `RESEARCH <inquiry_id>` with no description; answer subheadings are allowed; scores remain exactly two numeric lines.
4. Add focused parser fixtures covering accepted nested answer headings and rejected control-section headings, unknown/duplicate H1 sections, decorated commands, and missing/invalid scores. Do not edit generated replies or reclassify historical work.
5. The coordinator runs the relevant parser/research checks once, then builds. This change performs no test execution, provider call, launch, retry, or experimental state mutation.
