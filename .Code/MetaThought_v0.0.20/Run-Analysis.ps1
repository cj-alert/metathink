#requires -Version 7.0
[CmdletBinding()]
param(
 [Parameter(Mandatory)][ValidateSet('Base','Evolution','Planning','Status')][string]$Mode,
 [Parameter(Mandatory)][string]$RunFolder,
 [string]$RatesPath,
 [string]$OutputPrefix,
 [string]$PythonPath
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if (-not $PythonPath) { $PythonPath = [Environment]::GetEnvironmentVariable('FOUNDEROS_PYTHON','User') }
if (-not $PythonPath -or -not (Test-Path -LiteralPath $PythonPath -PathType Leaf)) { throw 'Supply the verified Python executable with -PythonPath.' }
$PythonPath = [IO.Path]::GetFullPath($PythonPath)
$runPath = (Get-Item -LiteralPath $RunFolder -ErrorAction Stop).FullName
if ($Mode -eq 'Status') {
 $pythonVersion = & $PythonPath --version
 if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
 & $PythonPath -B (Join-Path $PSScriptRoot 'Analyze-Evolution.py') --status $runPath
 exit $LASTEXITCODE
}
if (-not $RatesPath -or -not $OutputPrefix) { throw 'Report modes require RatesPath and OutputPrefix.' }
$ratesFile = (Get-Item -LiteralPath $RatesPath -ErrorAction Stop).FullName
$prefix = [IO.Path]::GetFullPath($OutputPrefix)
if (-not (Test-Path -LiteralPath ([IO.Path]::GetDirectoryName($prefix)) -PathType Container)) { throw 'Output parent must already exist.' }
$stdoutPath = $prefix + '.analysis.stdout.txt'
$stderrPath = $prefix + '.analysis.stderr.txt'
$receiptPath = $prefix + '.analysis.receipt.json'
foreach ($path in @($stdoutPath,$stderrPath,$receiptPath,($prefix+'.json'),($prefix+'.md'))) {
 if (Test-Path -LiteralPath $path) { throw "Preserve existing analysis output: $path" }
}
$scriptName = 'Analyze-SavedExperiment.py'
if ($Mode -eq 'Evolution') { $scriptName = 'Analyze-Evolution.py' }
if ($Mode -eq 'Planning') { $scriptName = 'Analyze-Planning.py' }
$scriptPath = Join-Path $PSScriptRoot $scriptName
$pythonVersion = & $PythonPath --version
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$arguments = @('-B',$scriptPath,$runPath,$ratesFile,$prefix)
$started = [DateTime]::UtcNow
& $PythonPath @arguments 1> $stdoutPath 2> $stderrPath
$analysisExit = $LASTEXITCODE
$finished = [DateTime]::UtcNow
$receipt = [ordered]@{mode=$Mode;executable=$PythonPath;runtime=$pythonVersion;arguments=$arguments;started_utc=$started.ToString('o');finished_utc=$finished.ToString('o');elapsed_seconds=($finished-$started).TotalSeconds;exit_code=$analysisExit;stdout=$stdoutPath;stderr=$stderrPath;source_databases_read_only=$true;provider_calls=$false}
[IO.File]::WriteAllText($receiptPath,($receipt | ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
[Console]::Out.Write([IO.File]::ReadAllText($stdoutPath))
[Console]::Error.Write([IO.File]::ReadAllText($stderrPath))
[ordered]@{exit_code=$analysisExit;receipt=$receiptPath;json=$prefix+'.json';markdown=$prefix+'.md'} | ConvertTo-Json -Compress
exit $analysisExit
