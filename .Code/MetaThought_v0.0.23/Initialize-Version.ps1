#requires -Version 7.0
[CmdletBinding()]
param([Parameter(Mandatory)][string]$SourceDirectory)
$ErrorActionPreference = 'Stop'
$source = (Resolve-Path -LiteralPath $SourceDirectory).Path
$destination = $PSScriptRoot
$existing = @(Get-ChildItem -LiteralPath $destination -Force | Where-Object Name -ne 'Initialize-Version.ps1')
if ($existing.Count) { throw 'Version destination already contains files; preserve it.' }
$exclude = @('distribution-suite.json','Run-DistributionObservation.ps1','Resume-DistributionObservation.ps1','v014_rotation_test.go','v016_newborn_modes_test.go')
$files = @(Get-ChildItem -LiteralPath $source -File | Where-Object { $_.Extension -in '.go','.json','.md','.ps1','.py','.mod','.sum' -and $_.Name -notin $exclude -and $_.Name -notlike 'variant-*.json' -and $_.Name -ne 'Initialize-Version.ps1' })
foreach ($file in $files) { Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $destination $file.Name) -ErrorAction Stop }
[ordered]@{source=$source;destination=$destination;files=$files.Name;excluded=$exclude;created_utc=[DateTime]::UtcNow.ToString('o');runtime_state_copied=$false} | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $destination 'version-origin.json') -Encoding utf8NoBOM
[ordered]@{destination=$destination;copied_files=$files.Count;runtime_state_copied=$false} | ConvertTo-Json -Compress
