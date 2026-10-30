#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [Parameter(Mandatory)][string]$OutputPrefix,
    [string]$PythonPath
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if (-not $PythonPath) { $PythonPath = [Environment]::GetEnvironmentVariable('FOUNDEROS_PYTHON','User') }
if (-not $PythonPath -or -not (Test-Path -LiteralPath $PythonPath -PathType Leaf)) {
    $knownPython = Join-Path $env:LOCALAPPDATA 'Programs/Python/Python314/python.exe'
    if (Test-Path -LiteralPath $knownPython -PathType Leaf) { $PythonPath = $knownPython }
    else { throw 'Supply the verified Python executable with -PythonPath or configure FOUNDEROS_PYTHON.' }
}
$PythonPath = [IO.Path]::GetFullPath($PythonPath)
$runPath = (Get-Item -LiteralPath $RunFolder -ErrorAction Stop).FullName
if (-not (Test-Path -LiteralPath $runPath -PathType Container)) { throw 'RunFolder must be a directory.' }
$prefix = [IO.Path]::GetFullPath($OutputPrefix)
if (-not (Test-Path -LiteralPath ([IO.Path]::GetDirectoryName($prefix)) -PathType Container)) { throw 'Output parent must already exist.' }
$stdoutPath = $prefix + '.analysis.stdout.txt'
$stderrPath = $prefix + '.analysis.stderr.txt'
$receiptPath = $prefix + '.analysis.receipt.json'
foreach ($path in @($stdoutPath,$stderrPath,$receiptPath,($prefix+'.json'),($prefix+'.md'))) {
    if (Test-Path -LiteralPath $path) { throw "Preserve existing analysis output: $path" }
}
$scriptPath = Join-Path $PSScriptRoot 'Analyze-Inquiries.py'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) { throw 'Inquiry extractor is missing.' }
$arguments = @('-B',$scriptPath,$runPath,$prefix)
$started = [DateTime]::UtcNow
$analysisExit = -1
$pythonVersion = $null
$invocationError = $null
try {
    $pythonVersion = & $PythonPath --version
    if ($LASTEXITCODE -ne 0) { throw 'Selected Python executable did not report its runtime version.' }
    & $PythonPath -B -c 'import sqlite3, json, argparse, contextlib, collections, datetime, pathlib'
    if ($LASTEXITCODE -ne 0) { throw 'Required Python standard-library imports failed.' }
    & $PythonPath @arguments 1> $stdoutPath 2> $stderrPath
    $analysisExit = $LASTEXITCODE
}
catch { $invocationError = $_.Exception.Message }
finally {
    $finished = [DateTime]::UtcNow
    $receipt = [ordered]@{
        version='0.0.13'; mode='Worker inquiry'; executable=$PythonPath; runtime=$pythonVersion; arguments=$arguments;
        started_utc=$started.ToString('o'); finished_utc=$finished.ToString('o'); elapsed_seconds=($finished-$started).TotalSeconds;
        exit_code=$analysisExit; invocation_error=$invocationError; stdout=$stdoutPath; stderr=$stderrPath;
        source_databases_read_only=$true; provider_calls=$false; cost_estimates=$false
    }
    $stream = [IO.File]::Open($receiptPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try {
        $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($receipt | ConvertTo-Json -Depth 5))
        $stream.Write($bytes, 0, $bytes.Length)
    }
    finally { $stream.Dispose() }
}
if (Test-Path -LiteralPath $stdoutPath -PathType Leaf) { [Console]::Out.Write([IO.File]::ReadAllText($stdoutPath)) }
if (Test-Path -LiteralPath $stderrPath -PathType Leaf) { [Console]::Error.Write([IO.File]::ReadAllText($stderrPath)) }
if ($invocationError) { [Console]::Error.WriteLine($invocationError) }
[ordered]@{exit_code=$analysisExit;receipt=$receiptPath;json=$prefix+'.json';markdown=$prefix+'.md'} | ConvertTo-Json -Compress
exit $analysisExit
