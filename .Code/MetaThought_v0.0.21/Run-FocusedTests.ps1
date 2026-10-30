#requires -Version 7.0
[CmdletBinding()]
param(
 [Parameter(Mandatory)][string]$EvidenceFolder,
 [ValidateSet('v007','v008','all')][string]$VersionFilter = 'v008',
 [string]$TestPattern
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if (-not $TestPattern) {
 $TestPattern = switch ($VersionFilter) { 'v007' { '^TestV007' } 'v008' { '^TestV008' } 'all' { '^TestV00[78]' } }
}
$goExe = 'C:\Program Files\Go\bin\go.exe'
$goFmtExe = 'C:\Program Files\Go\bin\gofmt.exe'
foreach ($tool in @($goExe, $goFmtExe)) {
 if (-not (Test-Path -LiteralPath $tool -PathType Leaf)) { throw "Required local Go tool missing: $tool" }
}
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
$env:CGO_ENABLED = '0'
$env:GOFLAGS = '-buildvcs=false'
$evidencePath = [IO.Path]::GetFullPath($EvidenceFolder)
if (Test-Path -LiteralPath $evidencePath) {
 if (-not (Test-Path -LiteralPath $evidencePath -PathType Container)) { throw 'EvidenceFolder is not a directory.' }
} else { [IO.Directory]::CreateDirectory($evidencePath) | Out-Null }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffffffZ')
$stdoutPath = Join-Path $evidencePath "$stamp.tests.jsonl"
$stderrPath = Join-Path $evidencePath "$stamp.stderr.txt"
$receiptPath = Join-Path $evidencePath "$stamp.receipt.json"
foreach ($path in @($stdoutPath,$stderrPath,$receiptPath)) {
 if (Test-Path -LiteralPath $path) { throw "Preserve existing test artifact: $path" }
}
Push-Location -LiteralPath $PSScriptRoot
try {
 $production = @(Get-ChildItem -LiteralPath $PSScriptRoot -File -Filter '*.go' | Where-Object { $_.Name -notlike '*_test.go' } | Sort-Object Name | Select-Object -ExpandProperty Name)
 $focused = @(Get-ChildItem -LiteralPath $PSScriptRoot -File -Filter '*_test.go' | Where-Object {
  if ($VersionFilter -eq 'v008') { $_.Name -like 'v008_*_test.go' -or $_.Name -in @('v007_helpers_test.go','v007_base_contract_test.go') }
  elseif ($VersionFilter -eq 'v007') { $_.Name -like 'v007_*_test.go' }
  else { $_.Name -like 'v007_*_test.go' -or $_.Name -like 'v008_*_test.go' }
 } | Sort-Object Name | Select-Object -ExpandProperty Name)
 if ($production.Count -eq 0 -or $focused.Count -eq 0) { throw 'Production sources and focused fixtures are required.' }
 & $goFmtExe -w @focused
 if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
 $sdkVersion = & $goExe version
 if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
 $arguments = @('test','-json','-count=1','-run',$TestPattern) + $production + $focused
 $started = [DateTime]::UtcNow
 & $goExe @arguments 1> $stdoutPath 2> $stderrPath
 $testExit = $LASTEXITCODE
 $finished = [DateTime]::UtcNow
 $receipt = [ordered]@{
  version='0.0.8'; scope='focused local tests'; version_filter=$VersionFilter; go_executable=$goExe; sdk=$sdkVersion;
  working_directory=$PSScriptRoot; arguments=$arguments; started_utc=$started.ToString('o'); finished_utc=$finished.ToString('o');
  elapsed_seconds=($finished-$started).TotalSeconds; exit_code=$testExit;
  stdout=$stdoutPath; stderr=$stderrPath; simulated_models=$true; http_blocked_by_test_main=$true;
  historical_test_files_excluded=$true; module_downloads_disabled=$true
 }
 [IO.File]::WriteAllText($receiptPath, ($receipt | ConvertTo-Json -Depth 6), [Text.UTF8Encoding]::new($false))
 [ordered]@{exit_code=$testExit;elapsed_seconds=$receipt.elapsed_seconds;sdk=$sdkVersion;stdout=$stdoutPath;stderr=$stderrPath;receipt=$receiptPath} | ConvertTo-Json -Compress
 if ($testExit -ne 0) { [Console]::Error.Write([IO.File]::ReadAllText($stderrPath)) }
}
finally { Pop-Location }
exit $testExit
