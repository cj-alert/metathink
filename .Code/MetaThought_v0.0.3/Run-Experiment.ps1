param(
 [Parameter(Mandatory=$true)][string]$RunFolder,
 [ValidateSet("init","decisions","plan","run","resume","analyze")][string]$Stage = "init",
 [string]$ConfigPath,
 [long]$RequestID = 0,
 [string]$ExecutablePath,
 [string]$AttemptLabel
)
$ErrorActionPreference = "Stop"
$explicitConfig = [bool]$ConfigPath
if (-not $ConfigPath) { $ConfigPath = Join-Path $PSScriptRoot "config.json" }
$runPath = [IO.Path]::GetFullPath($RunFolder)
if ($Stage -eq "init") {
 if (Test-Path -LiteralPath $runPath) { throw "Preserve existing experiment folder; use a new name." }
 New-Item -ItemType Directory -Path $runPath | Out-Null
 Copy-Item -LiteralPath (Join-Path $PSScriptRoot "MetaThought.exe") -Destination (Join-Path $runPath "MetaThought.exe")
 Copy-Item -LiteralPath (Join-Path $PSScriptRoot "decisions_smoke.json") -Destination (Join-Path $runPath "decisions_smoke.json")
}
$exe = Join-Path $runPath "MetaThought.exe"
if ($ExecutablePath) { $exe = [IO.Path]::GetFullPath($ExecutablePath) }
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Run executable missing; initialize first." }
$outputLabel = $Stage
if ($AttemptLabel) {
 if ([IO.Path]::GetFileName($AttemptLabel) -ne $AttemptLabel) { throw "Attempt label must be a flat name." }
 $outputLabel = $AttemptLabel
}
$outPath = Join-Path $runPath ($outputLabel + ".stdout.txt")
$errPath = Join-Path $runPath ($outputLabel + ".stderr.txt")
if ((Test-Path -LiteralPath $outPath) -or (Test-Path -LiteralPath $errPath)) { throw "Stage output exists; preserve it and use a new experiment for retry." }
$runConfig = Join-Path $runPath "config.json"
if ($explicitConfig -and $Stage -ne "init") { $runConfig = [IO.Path]::GetFullPath($ConfigPath) }
$stepArgs = @()
switch ($Stage) {
 "init" { $stepArgs = @("init","-config",$ConfigPath,"-dir",$runPath) }
 "decisions" { $stepArgs = @("decide","-config",$runConfig,"-dir",$runPath,"-request",(Join-Path $runPath "decisions_smoke.json")) }
 "plan" { $stepArgs = @("plan","-config",$runConfig,"-dir",$runPath) }
 "run" { $stepArgs = @("run","-config",$runConfig,"-dir",$runPath,"-contract",(Join-Path $runPath "contract.md")) }
 "resume" { if ($RequestID -lt 1) { throw "Resume requires RequestID." }; $stepArgs = @("resume","-config",$runConfig,"-dir",$runPath,"-request-id",[string]$RequestID) }
 "analyze" { $stepArgs = @("analyze","-config",$runConfig,"-dir",$runPath) }
}
& $exe @stepArgs 1> $outPath 2> $errPath
$stepExit = $LASTEXITCODE
[ordered]@{stage=$Stage;exit_code=$stepExit;stdout=$outPath;stderr=$errPath} | ConvertTo-Json -Compress
if ($stepExit -ne 0) { Get-Content -LiteralPath $errPath }
exit $stepExit
