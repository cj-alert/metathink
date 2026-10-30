#requires -Version 7.0
[CmdletBinding()]
param(
 [ValidateSet('Prepare','Reply')][string]$Stage = 'Prepare',
 [Parameter(Mandatory)][string]$EvidenceFolder,
 [string]$SourceRun,
 [string]$ExportFolder,
 [long]$WorkID,
 [string]$ReplyFolder,
 [string]$ConfigPath = (Join-Path $PSScriptRoot 'config.json')
)
$ErrorActionPreference = 'Stop'
if ($Stage -eq 'Prepare') {
 if (-not $SourceRun -or -not $ExportFolder -or $WorkID -lt 1) { throw 'Prepare requires SourceRun, fresh ExportFolder and positive WorkID.' }
 $env:METATHOUGHT_QUEEN_SOURCE = [IO.Path]::GetFullPath($SourceRun)
 $env:METATHOUGHT_QUEEN_EXPORT = [IO.Path]::GetFullPath($ExportFolder)
 $env:METATHOUGHT_QUEEN_CONFIG = [IO.Path]::GetFullPath($ConfigPath)
 $env:METATHOUGHT_QUEEN_WORK = [string]$WorkID
 $pattern = '^TestV010(QueenContextReports|QueenContextSaved|QueenNoop)'
} else {
 if (-not $ReplyFolder) { throw 'Reply requires ReplyFolder.' }
 $env:METATHOUGHT_QUEEN_REPLY_FOLDER = [IO.Path]::GetFullPath($ReplyFolder)
 $pattern = '^TestV010QueenContextReply$'
}
& (Join-Path $PSScriptRoot 'Run-WorkerTests.ps1') -EvidenceFolder $EvidenceFolder -TestPattern $pattern
exit $LASTEXITCODE
