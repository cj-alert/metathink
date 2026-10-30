param(
 [Parameter(Mandatory=$true)][string]$RunFolder,
 [Parameter(Mandatory=$true)][string]$BaselineFolder,
 [long]$BaselineRequestID = 1,
 [string]$PriorAssessment,
 [string]$ConfigPath
)
$ErrorActionPreference = "Stop"
if (-not $ConfigPath) { $ConfigPath = Join-Path $PSScriptRoot "config.json" }
$runPath = [IO.Path]::GetFullPath($RunFolder)
$baselinePath = [IO.Path]::GetFullPath($BaselineFolder)
if (Test-Path -LiteralPath $runPath) { throw "Preserve existing experiment folder; choose a new RunFolder." }
if (-not (Test-Path -LiteralPath $baselinePath -PathType Container)) { throw "Baseline folder is missing." }
$sourceExe = Join-Path $PSScriptRoot "MetaThought.exe"
if (-not (Test-Path -LiteralPath $sourceExe -PathType Leaf)) { throw "Build MetaThought.exe before the experiment." }
New-Item -ItemType Directory -Path $runPath | Out-Null
$exe = Join-Path $runPath "MetaThought.exe"
Copy-Item -LiteralPath $sourceExe -Destination $exe
$stepArgs = @("experiment", "-config", [IO.Path]::GetFullPath($ConfigPath), "-dir", $runPath, "-baseline", $baselinePath, "-baseline-request", [string]$BaselineRequestID)
if ($PriorAssessment) { $stepArgs += @("-prior", [IO.Path]::GetFullPath($PriorAssessment)) }
$outPath = Join-Path $runPath "experiment.stdout.txt"
$errPath = Join-Path $runPath "experiment.stderr.txt"
& $exe @stepArgs 1> $outPath 2> $errPath
$stepExit = $LASTEXITCODE
[ordered]@{stage="experiment";exit_code=$stepExit;stdout=$outPath;stderr=$errPath} | ConvertTo-Json -Compress
if ($stepExit -ne 0) { Get-Content -LiteralPath $errPath }
exit $stepExit
