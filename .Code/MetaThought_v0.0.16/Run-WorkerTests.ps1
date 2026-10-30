#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$EvidenceFolder,
    [string]$TestPattern = '^TestV0(10|14|15|16)'
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$goExe = 'C:\Program Files\Go\bin\go.exe'
if (-not (Test-Path -LiteralPath $goExe -PathType Leaf)) { throw "Required local Go executable is missing: $goExe" }
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
$env:CGO_ENABLED = '0'
$env:GOFLAGS = '-buildvcs=false'
$evidencePath = [IO.Path]::GetFullPath($EvidenceFolder)
if (Test-Path -LiteralPath $evidencePath) {
    if (-not (Test-Path -LiteralPath $evidencePath -PathType Container)) { throw 'EvidenceFolder must be a directory.' }
}
else { [IO.Directory]::CreateDirectory($evidencePath) | Out-Null }
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffffffZ')
$stdoutPath = Join-Path $evidencePath "$stamp.tests.jsonl"
$stderrPath = Join-Path $evidencePath "$stamp.stderr.txt"
$receiptPath = Join-Path $evidencePath "$stamp.receipt.json"
foreach ($path in @($stdoutPath, $stderrPath, $receiptPath)) {
    if (Test-Path -LiteralPath $path) { throw "Preserve existing local test evidence: $path" }
}
$production = @(Get-ChildItem -LiteralPath $PSScriptRoot -File -Filter '*.go' | Where-Object { $_.Name -notlike '*_test.go' } | Sort-Object Name | Select-Object -ExpandProperty Name)
$support = @('v009_inquiry_test.go')
foreach ($name in $support) {
    if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot $name) -PathType Leaf)) { throw "Required shared fixture is missing: $name" }
}
$focused = @(Get-ChildItem -LiteralPath $PSScriptRoot -File | Where-Object { $_.Name -match '^v0(10|14|15|16)_.*_test\.go$' } | Sort-Object Name | Select-Object -ExpandProperty Name)
if ($production.Count -eq 0 -or $focused.Count -eq 0) { throw 'Production source and v010 fixtures are required.' }
$arguments = @('test','-json','-count=1','-timeout=0','-run',$TestPattern) + $production + $support + $focused
$started = [DateTime]::UtcNow
$testExit = -1
$invocationError = $null
$sdkVersion = $null
Push-Location -LiteralPath $PSScriptRoot
try {
    $sdkVersion = & $goExe version
    if ($LASTEXITCODE -ne 0) { throw 'The selected Go executable did not report its SDK version.' }
    & $goExe @arguments 1> $stdoutPath 2> $stderrPath
    $testExit = $LASTEXITCODE
}
catch { $invocationError = $_.Exception.Message }
finally {
    Pop-Location
    $finished = [DateTime]::UtcNow
    $receipt = [ordered]@{
        version='0.0.16'; scope='focused local worker tests'; go_executable=$goExe; sdk=$sdkVersion;
        working_directory=$PSScriptRoot; arguments=$arguments;
        started_utc=$started.ToString('o'); finished_utc=$finished.ToString('o'); elapsed_seconds=($finished-$started).TotalSeconds;
        exit_code=$testExit; invocation_error=$invocationError; stdout=$stdoutPath; stderr=$stderrPath;
        support_files=$support; fixture_files=$focused; test_pattern=$TestPattern;
        network_guard='v009_inquiry_test.go TestMain blocks HTTP and fails on any attempt';
        other_historical_fixtures_excluded=$true; module_downloads_disabled=$true
    }
    $stream = [IO.File]::Open($receiptPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try {
        $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($receipt | ConvertTo-Json -Depth 6))
        $stream.Write($bytes, 0, $bytes.Length)
    }
    finally { $stream.Dispose() }
}
[ordered]@{exit_code=$testExit;elapsed_seconds=$receipt.elapsed_seconds;stdout=$stdoutPath;stderr=$stderrPath;receipt=$receiptPath} | ConvertTo-Json -Compress
if ($invocationError) { [Console]::Error.WriteLine($invocationError) }
if ($testExit -ne 0 -and (Test-Path -LiteralPath $stderrPath -PathType Leaf)) { [Console]::Error.Write([IO.File]::ReadAllText($stderrPath)) }
exit $testExit
