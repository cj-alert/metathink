# HTTP 500 recovery and notification

## Runtime behavior
The configured Meta adapter retries a completely received HTTP 500 response using the same serialized request and endpoint. `meta.http_500_retry.max_attempts` is 3 and `delay_ms` is 30000. Each failed response, delay, attempt and final outcome is logged. Other status codes, uncertain transport/read failures, malformed completions and refusals retain their existing handling.

After exhaustion, new dispatch stops and active calls drain. Completed work is retained. Only claims with known exhausted HTTP 500 failures are released automatically. The worker result records `status: provider_unavailable`, `provider_status_code: 500`, the attempt count and error, plus `ready_for_resume: true` after the saved drain completes. Exploration resumes its queue phase; convergence resumes and reuses completed reports/assessments.

## Resume and alerts
Use `Resume-WorkerExperiment.ps1 -RunFolder <folder> -Stage Run` for a controlled provider stop. No `-AllowFailed` or `worker-recover` is needed when ready_for_resume is true. Ordinary failed states still require their existing explicit review/recovery. Do not start a second process against a running hive.

Run-WorkerExperiment.ps1 and Resume-WorkerExperiment.ps1 call Send-WorkerAlert.ps1 after saving the stopped result. The configured alerts.http_500_sms object supplies enabled, helper_path, project and chat_label. The helper uses the canonical notification-only SMS process; no credentials are copied into the hive. It reserves one receipt before sending, never retries an ambiguous send, and cannot overwrite the worker result. Use these launchers for the SMS notification path. Direct native work commands do not invoke the PowerShell notifier.

## Verification
Run-WorkerTests.ps1 selects focused injected-HTTP and real-SQLite checks. Run-WorkerAlertTests.ps1 uses fake SMS helpers only. The canonical SMS helper supports ValidateOnly for local configuration validation without sending. Preserve all failed results and diagnostics; never rewrite generated model outputs.
