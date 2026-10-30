#requires -Version 7.0
param(
 [Parameter(Mandatory=$true)][string]$RunFolder,
 [ValidateSet("Draft","Start")][string]$Stage = "Draft",
 [string]$BaselineFolder,
 [ValidateRange(1, 9223372036854775807)][long]$BaselineRequestID = 1,
 [string]$MetaContract,
 [switch]$Confirm,
 [string]$PriorAssessment,
 [string]$ConfigPath = (Join-Path $PSScriptRoot "config.json"),
 [string]$ExecutablePath = (Join-Path $PSScriptRoot "MetaThought.exe")
)
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false
if ($Stage -eq "Start") {
 if (-not $MetaContract -or -not $Confirm -or -not $BaselineFolder) {
  throw "Start requires -BaselineFolder, an explicitly reviewed -MetaContract path, and -Confirm."
 }
 $MetaContract = [IO.Path]::GetFullPath($MetaContract)
 $baselinePath = [IO.Path]::GetFullPath($BaselineFolder)
 if (-not (Test-Path -LiteralPath $MetaContract -PathType Leaf)) { throw "Reviewed meta contract file is missing." }
 if (-not (Test-Path -LiteralPath $baselinePath -PathType Container)) { throw "Baseline folder is missing." }
 if ($PriorAssessment) {
  $PriorAssessment = [IO.Path]::GetFullPath($PriorAssessment)
  if (-not (Test-Path -LiteralPath $PriorAssessment -PathType Leaf)) { throw "Prior assessment file is missing." }
 }
} elseif ($MetaContract -or $Confirm -or $BaselineFolder -or $PriorAssessment) {
 throw "Draft only creates a meta contract. Use -Stage Start with the reviewed contract and confirmation to select a baseline."
}
$sourceConfig = (Get-Item -LiteralPath $ConfigPath -ErrorAction Stop).FullName
if (-not (Test-Path -LiteralPath $sourceConfig -PathType Leaf)) { throw "Configuration is not a file." }
$settings = Get-Content -LiteralPath $sourceConfig -Raw | ConvertFrom-Json
$runPath = [IO.Path]::GetFullPath($RunFolder)
if ((Test-Path -LiteralPath $runPath) -and -not (Test-Path -LiteralPath $runPath -PathType Container)) { throw "RunFolder is not a directory." }
foreach ($name in @($settings.files.state, $settings.files.result, $settings.files.contract, "config.json", "mq_config.json", "comparison.md")) {
 if (Test-Path -LiteralPath (Join-Path $runPath $name)) { throw "Active or completed experiment artifacts already exist; preserve this folder and choose a new one." }
}
if ($Stage -eq "Draft" -and (Test-Path -LiteralPath (Join-Path $runPath "meta-contract.md"))) { throw "Meta draft already exists; preserve it and use another draft folder." }
if (-not (Test-Path -LiteralPath $runPath)) { [IO.Directory]::CreateDirectory($runPath) | Out-Null }
$exe = Join-Path $runPath "MetaThought.exe"
if (-not (Test-Path -LiteralPath $exe)) {
 $sourceExe = (Get-Item -LiteralPath $ExecutablePath -ErrorAction Stop).FullName
 if (-not (Test-Path -LiteralPath $sourceExe -PathType Leaf)) { throw "Build MetaThought.exe before drafting." }
 [IO.File]::Copy($sourceExe, $exe, $false)
} elseif (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Saved executable is not a file." }
$commandName = "meta-plan"
$stepArgs = @($commandName, "-config", $sourceConfig, "-dir", $runPath)
if ($Stage -eq "Start") {
 $commandName = "experiment"
 $stepArgs = @($commandName, "-config", $sourceConfig, "-dir", $runPath, "-baseline", $baselinePath,
  "-baseline-request", [string]$BaselineRequestID, "-meta-contract", $MetaContract, "-confirm")
 if ($PriorAssessment) { $stepArgs += @("-prior", $PriorAssessment) }
}
$stamp = [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssfffffffZ")
$outPath = Join-Path $runPath "$commandName-$stamp.stdout.txt"
$errPath = Join-Path $runPath "$commandName-$stamp.stderr.txt"
$exitPath = Join-Path $runPath "$commandName-$stamp.exitcode.txt"
foreach ($path in @($outPath, $errPath, $exitPath)) {
 if (Test-Path -LiteralPath $path) { throw "Invocation output already exists: $path" }
}
& $exe @stepArgs 1> $outPath 2> $errPath
$stepExit = $LASTEXITCODE
[IO.File]::WriteAllText($exitPath, [string]$stepExit, [Text.UTF8Encoding]::new($false))
[ordered]@{stage=$Stage;exit_code=$stepExit;stdout=$outPath;stderr=$errPath} | ConvertTo-Json -Compress
if ($stepExit -ne 0) { Get-Content -LiteralPath $errPath }
elseif ($Stage -eq "Draft") { Write-Output "Stopped after meta drafting. Review meta-contract.md; no experiment has started." }
exit $stepExit
