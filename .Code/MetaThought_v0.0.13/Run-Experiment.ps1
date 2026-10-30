param(
 [Parameter(Mandatory=$true)][string]$RunFolder,
 [ValidateSet("draft","init","decisions","plan","run","resume","analyze")][string]$Stage = "draft",
 [string]$ConfigPath,
 [string]$Contract,
 [switch]$Confirm,
 [long]$RequestID = 0,
 [string]$ExecutablePath,
 [string]$AttemptLabel
)
$ErrorActionPreference = "Stop"
if ($Stage -in @("draft", "plan")) {
 throw "Planning moved to Run-Planning.ps1 -RunFolder <new-folder> -QuestionPath <exact-question-file>. It saves a proposal without initializing or running a hive. Use that launcher with -Stage revise and a new -DraftName for feedback."
}
if ($Stage -eq "run") {
 if (-not $Contract -or -not $Confirm) { throw "Run requires an explicitly reviewed -Contract path and -Confirm." }
 $Contract = [IO.Path]::GetFullPath($Contract)
 if (-not (Test-Path -LiteralPath $Contract -PathType Leaf)) { throw "Confirmed contract file missing: $Contract" }
} elseif ($Contract -or $Confirm) {
 throw "-Contract and -Confirm apply only to -Stage run. Drafting never confirms or executes a request."
}
if ($Stage -eq "resume" -and $RequestID -lt 1) { throw "Resume requires RequestID." }
if ($AttemptLabel -and [IO.Path]::GetFileName($AttemptLabel) -ne $AttemptLabel) { throw "Attempt label must be a flat name." }
$explicitConfig = [bool]$ConfigPath
if (-not $ConfigPath) { $ConfigPath = Join-Path $PSScriptRoot "config.json" }
$ConfigPath = [IO.Path]::GetFullPath($ConfigPath)
$runPath = [IO.Path]::GetFullPath($RunFolder)
if ($Stage -in @("draft", "init")) {
 if (Test-Path -LiteralPath $runPath) { throw "Preserve existing experiment folder; use a new name." }
 $sourceExe = Join-Path $PSScriptRoot "MetaThought.exe"
 if ($ExecutablePath) { $sourceExe = [IO.Path]::GetFullPath($ExecutablePath) }
 if (-not (Test-Path -LiteralPath $sourceExe -PathType Leaf)) { throw "Source executable missing; compile first." }
 New-Item -ItemType Directory -Path $runPath | Out-Null
 Copy-Item -LiteralPath $sourceExe -Destination (Join-Path $runPath "MetaThought.exe")
 Copy-Item -LiteralPath (Join-Path $PSScriptRoot "decisions_smoke.json") -Destination (Join-Path $runPath "decisions_smoke.json")
}
$exe = Join-Path $runPath "MetaThought.exe"
if ($ExecutablePath -and $Stage -notin @("draft", "init")) { $exe = [IO.Path]::GetFullPath($ExecutablePath) }
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Run executable missing; initialize first." }
$runConfig = Join-Path $runPath "config.json"
if ($explicitConfig -and $Stage -notin @("draft", "init")) { $runConfig = $ConfigPath }
$phases = @($Stage)
if ($Stage -eq "draft") { $phases = @("init", "plan") }
foreach ($phase in $phases) {
 $outputLabel = $phase
 if ($AttemptLabel) {
  $outputLabel = $AttemptLabel
  if ($Stage -eq "draft") { $outputLabel += "." + $phase }
 }
 $outPath = Join-Path $runPath ($outputLabel + ".stdout.txt")
 $errPath = Join-Path $runPath ($outputLabel + ".stderr.txt")
 if ((Test-Path -LiteralPath $outPath) -or (Test-Path -LiteralPath $errPath)) { throw "Stage output exists; preserve it and choose a new AttemptLabel for an authorized retry." }
 $stepArgs = @()
 switch ($phase) {
  "init" { $stepArgs = @("init","-config",$ConfigPath,"-dir",$runPath) }
  "decisions" { $stepArgs = @("decide","-config",$runConfig,"-dir",$runPath,"-request",(Join-Path $runPath "decisions_smoke.json")) }
  "plan" { $stepArgs = @("plan","-config",$runConfig,"-dir",$runPath) }
  "run" { $stepArgs = @("run","-config",$runConfig,"-dir",$runPath,"-contract",$Contract,"-confirm") }
  "resume" { $stepArgs = @("resume","-config",$runConfig,"-dir",$runPath,"-request-id",[string]$RequestID) }
  "analyze" { $stepArgs = @("analyze","-config",$runConfig,"-dir",$runPath) }
 }
 & $exe @stepArgs 1> $outPath 2> $errPath
 $stepExit = $LASTEXITCODE
 [ordered]@{stage=$phase;exit_code=$stepExit;stdout=$outPath;stderr=$errPath} | ConvertTo-Json -Compress
 if ($stepExit -ne 0) { Get-Content -LiteralPath $errPath; exit $stepExit }
}
if ($Stage -in @("draft", "plan")) {
 Write-Output ("Stopped after drafting. Review " + (Join-Path $runPath "contract.md") + "; no hive request has run. A separately approved run requires -Stage run -Contract <reviewed-file> -Confirm.")
}
exit 0
