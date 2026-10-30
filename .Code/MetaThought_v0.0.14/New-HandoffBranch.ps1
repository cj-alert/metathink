#requires -Version 7.0
[CmdletBinding()]
param(
 [Parameter(Mandatory)][string]$CheckpointFolder,
 [Parameter(Mandatory)][string]$RunFolder,
 [string]$ChromosomesPath
)
$ErrorActionPreference = 'Stop'
$checkpoint = [IO.Path]::GetFullPath($CheckpointFolder)
$destination = [IO.Path]::GetFullPath($RunFolder)
if (Test-Path -LiteralPath $destination) { throw 'Experiment folder already exists. Preserve it; use a fresh name.' }
# Only operational inputs enter a new branch. Prior trial state and diagnostics are never imported.
$files = @('config.json','MetaThought.exe','question.txt','queen-intake-role.txt','intake.md','queen-handoff-role.txt','handoff-input.txt','handoff.md')
foreach ($name in $files) {
 if (-not (Test-Path -LiteralPath (Join-Path $checkpoint $name) -PathType Leaf)) { throw ('Required checkpoint input missing: ' + $name) }
}
$configText = $null
if ($ChromosomesPath) {
 $pairs = ConvertFrom-Json -InputObject ([IO.File]::ReadAllText([IO.Path]::GetFullPath($ChromosomesPath))) -AsHashtable -NoEnumerate
 if ($pairs -isnot [System.Collections.IList] -or $pairs.Count -eq 0) { throw 'Chromosomes must be a nonempty JSON array of contribute/expand pairs.' }
 foreach ($pair in $pairs) {
  if ($pair -isnot [System.Collections.IDictionary] -or $pair.Count -ne 2 -or -not $pair.Contains('contribute') -or -not $pair.Contains('expand')) { throw 'Each chromosome pair must contain only contribute and expand.' }
  foreach ($key in @('contribute','expand')) {
   if ($pair[$key] -isnot [string] -or [string]::IsNullOrWhiteSpace($pair[$key])) { throw 'Each chromosome instruction must be nonempty text.' }
  }
 }
 $config = [IO.File]::ReadAllText((Join-Path $checkpoint 'config.json')) | ConvertFrom-Json -AsHashtable
 $config.chromosomes = $pairs
 $configText = ConvertTo-Json -InputObject $config -Depth 100
}
[IO.Directory]::CreateDirectory($destination) | Out-Null
$utf8 = [Text.UTF8Encoding]::new($false)
$events = Join-Path $destination 'branch-preparation.jsonl'
foreach ($name in $files) {
 $source = Join-Path $checkpoint $name
 $target = Join-Path $destination $name
 if ($name -eq 'config.json' -and $null -ne $configText) {
  [IO.File]::WriteAllText($target, $configText, $utf8)
  $operation = 'config.write_chromosomes'
 } else {
  [IO.File]::Copy($source, $target, $false)
  $operation = 'file.copy'
 }
 $event = [ordered]@{ts=[DateTime]::UtcNow.ToString('o');event=$operation;source=$source;destination=$target}
 [IO.File]::AppendAllText($events, ($event | ConvertTo-Json -Compress) + [Environment]::NewLine, $utf8)
}
[ordered]@{status='prepared';checkpoint=$checkpoint;run=$destination;chromosomes_changed=[bool]$ChromosomesPath;model_calls=0;hive_started=$false} | ConvertTo-Json -Compress
