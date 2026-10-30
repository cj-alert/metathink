# Focused local v0.0.7 validation

Authorization October27: run answer-selection, interrupted-write recovery, and confirmed-contract tests at base and meta levels. No live Meta calls.

## Owning process
1. Add separate v007_*_test.go fixtures and Run-FocusedTests.ps1. Compile all production Go files plus only these focused fixtures; leave historical tests and saved runs intact. This is a scoped suite, not full-suite acceptance.
2. Inject TextClient/DecisionClient fixtures and isolated local SQLite stores. TestMain blocks HTTP transport and fails if any network attempt occurs. Fixture config removes the credential file and uses an isolated empty key variable.
3. Cover exact selected earlier/multiple answers and invalid/unassessed selections; force database statement failure inside score/return transactions; reopen stores and prove no half vector or repeated completed return. Include latest-run/trial/campaign recovery boundaries.
4. Give base/meta contracts criteria, targets and context different from config seeds. Observe prompts and saved state; verify actual scores belong to those confirmed elements, strict targets and disagreements are preserved, and resume reads saved authority. Exercise real orchestration where practical with simulated replies.
5. Save the Go JSON event stream, stderr, exact command and exit/duration in a new version-letter local-test folder. Preserve each failed attempt. Temporary test DBs are isolated and disposable; meaningful observations and failure diagnostics appear in test output. No historical experiment is opened writable.
6. Run once after authoring/review. Classify any failure; correct only an evidenced in-scope cause and rerun the affected check. Freeze passed checks. No live fallback, broad test sweep or repeat without a relevant change.
7. Record results and remaining limits. If production code changes, compile the corrected production binary separately; otherwise preserve the existing successful binary.
