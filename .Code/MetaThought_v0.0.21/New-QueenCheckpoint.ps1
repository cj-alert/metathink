#requires -Version 7.0
[CmdletBinding()]
param(
 [Parameter(Mandatory)][string]$CheckpointFolder,
 [Parameter(Mandatory)][string]$IntakeFolder,
 [Parameter(Mandatory)][string]$HandoffFolder,
 [string]$ConfigPath = (Join-Path $PSScriptRoot 'config.json'),
 [string]$ExecutablePath = (Join-Path $PSScriptRoot 'MetaThought.exe')
)
$ErrorActionPreference = 'Stop'
$destination = [IO.Path]::GetFullPath($CheckpointFolder)
if (Test-Path -LiteralPath $destination) { throw 'Checkpoint already exists. Preserve it; use a new checkpoint name.' }
$files = [ordered]@{
 'config.json' = $ConfigPath
 'MetaThought.exe' = $ExecutablePath
 'question.txt' = Join-Path $IntakeFolder 'prompt.txt'
 'queen-intake-role.txt' = Join-Path $IntakeFolder 'role.txt'
 'intake.md' = Join-Path $IntakeFolder 'reply.md'
 'intake-request.json' = Join-Path $IntakeFolder 'request.json'
 'intake-response.json' = Join-Path $IntakeFolder 'response.json'
 'intake-receipt.json' = Join-Path $IntakeFolder 'receipt.json'
 'intake-exchange.md' = Join-Path $IntakeFolder 'complete-exchange.md'
 'queen-handoff-role.txt' = Join-Path $HandoffFolder 'role.txt'
 'handoff-input.txt' = Join-Path $HandoffFolder 'prompt.txt'
 'handoff.md' = Join-Path $HandoffFolder 'reply.md'
 'handoff-request.json' = Join-Path $HandoffFolder 'request.json'
 'handoff-response.json' = Join-Path $HandoffFolder 'response.json'
 'handoff-receipt.json' = Join-Path $HandoffFolder 'receipt.json'
 'handoff-exchange.md' = Join-Path $HandoffFolder 'complete-exchange.md'
}
foreach ($entry in $files.GetEnumerator()) {
 if (-not (Test-Path -LiteralPath $entry.Value -PathType Leaf)) { throw ('Required checkpoint input missing: ' + $entry.Value) }
}
[IO.Directory]::CreateDirectory($destination) | Out-Null
$utf8 = [Text.UTF8Encoding]::new($false)
$events = Join-Path $destination 'checkpoint-preparation.jsonl'
foreach ($entry in $files.GetEnumerator()) {
 $source = [IO.Path]::GetFullPath($entry.Value)
 $target = Join-Path $destination $entry.Key
 [IO.File]::Copy($source, $target, $false)
 $event = [ordered]@{ts=[DateTime]::UtcNow.ToString('o');event='file.copy';source=$source;destination=$target}
 [IO.File]::AppendAllText($events, ($event | ConvertTo-Json -Compress) + [Environment]::NewLine, $utf8)
}
[ordered]@{status='frozen';checkpoint=$destination;model_calls=0;hive_started=$false} | ConvertTo-Json -Compress
