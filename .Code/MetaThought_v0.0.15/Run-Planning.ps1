#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [ValidateSet('new','revise')][string]$Stage = 'new',
    [ValidateSet('base','meta')][string]$Kind = 'base',
    [string]$QuestionPath,
    [string]$DraftName = 'proposal.md',
    [string]$PriorDraftPath,
    [string]$FeedbackPath,
    [string]$ConfigPath,
    [string]$ExecutablePath
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if (-not $DraftName -or [IO.Path]::GetFileName($DraftName) -ne $DraftName -or $DraftName -in @('.', '..')) {
    throw 'DraftName must be a new flat filename.'
}
$runPath = [IO.Path]::GetFullPath($RunFolder)
$savedConfig = Join-Path $runPath 'config.json'
$savedExe = Join-Path $runPath 'MetaThought.exe'
$savedQuestion = Join-Path $runPath 'question.txt'

# Resolve every source before changing the working directory. Copy bytes only;
# neither intake instructions nor the supplied question are rewritten here.
if ($Stage -eq 'new') {
    if (Test-Path -LiteralPath $runPath) { throw 'Preserve the existing planning folder; use a new folder or -Stage revise.' }
    if (-not $QuestionPath) { throw 'New planning requires -QuestionPath with the exact user question.' }
    if (-not $ConfigPath) { $ConfigPath = Join-Path $PSScriptRoot 'config.json' }
    if (-not $ExecutablePath) { $ExecutablePath = Join-Path $PSScriptRoot 'MetaThought.exe' }
    $sourceQuestion = (Get-Item -LiteralPath $QuestionPath -ErrorAction Stop).FullName
    $sourceConfig = (Get-Item -LiteralPath $ConfigPath -ErrorAction Stop).FullName
    $sourceExe = (Get-Item -LiteralPath $ExecutablePath -ErrorAction Stop).FullName
    foreach ($path in @($sourceQuestion, $sourceConfig, $sourceExe)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required planning input is not a file: $path" }
    }
    $settings = [IO.File]::ReadAllText($sourceConfig) | ConvertFrom-Json
}
else {
    if ($QuestionPath -or $ConfigPath -or $ExecutablePath) { throw 'Revision reuses the saved question, config and executable; supply only a new DraftName, PriorDraftPath and optional FeedbackPath.' }
    if (-not $PriorDraftPath) { throw 'Revision requires -PriorDraftPath and a new -DraftName.' }
    foreach ($path in @($savedConfig, $savedExe, $savedQuestion)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Saved planning input is missing: $path" }
    }
    $settings = [IO.File]::ReadAllText($savedConfig) | ConvertFrom-Json
    foreach ($name in @($settings.files.state, $settings.files.result, 'mq_config.json', 'comparison.md')) {
        if (Test-Path -LiteralPath (Join-Path $runPath $name)) { throw 'This folder contains an active inquiry; preserve it and use a new planning folder.' }
    }
}

$sourcePrior = $null
$sourceFeedback = $null
if ($PriorDraftPath) {
    $sourcePrior = (Get-Item -LiteralPath $PriorDraftPath -ErrorAction Stop).FullName
    if (-not (Test-Path -LiteralPath $sourcePrior -PathType Leaf)) { throw 'PriorDraftPath must be a file.' }
}
if ($FeedbackPath) {
    $sourceFeedback = (Get-Item -LiteralPath $FeedbackPath -ErrorAction Stop).FullName
    if (-not (Test-Path -LiteralPath $sourceFeedback -PathType Leaf)) { throw 'FeedbackPath must be a file.' }
}

$draftPath = Join-Path $runPath $DraftName
$stdoutPath = Join-Path $runPath "$DraftName.stdout.txt"
$stderrPath = Join-Path $runPath "$DraftName.stderr.txt"
$receiptPath = Join-Path $runPath "$DraftName.receipt.json"
$priorPath = Join-Path $runPath "$DraftName.prior.md"
$feedbackCopy = Join-Path $runPath "$DraftName.feedback.txt"
$reservedNames = @('config.json','MetaThought.exe','question.txt',$settings.files.state,$settings.files.debug,$settings.files.result,'mq_config.json','comparison.md')
$newPaths = @($draftPath, $stdoutPath, $stderrPath, $receiptPath)
if ($sourcePrior) { $newPaths += $priorPath }
if ($sourceFeedback) { $newPaths += $feedbackCopy }
foreach ($path in $newPaths) {
    if ([IO.Path]::GetFileName($path) -in $reservedNames) { throw "Planning output conflicts with a reserved file: $path" }
    if (Test-Path -LiteralPath $path) { throw "Preserve existing planning output and choose a new DraftName: $path" }
}

if ($Stage -eq 'new') {
    [IO.Directory]::CreateDirectory($runPath) | Out-Null
    [IO.File]::Copy($sourceConfig, $savedConfig, $false)
    [IO.File]::Copy($sourceExe, $savedExe, $false)
    [IO.File]::Copy($sourceQuestion, $savedQuestion, $false)
}
if ($sourcePrior) { [IO.File]::Copy($sourcePrior, $priorPath, $false) }
if ($sourceFeedback) { [IO.File]::Copy($sourceFeedback, $feedbackCopy, $false) }

$command = if ($Kind -eq 'meta') { 'meta-plan' } else { 'plan' }
$arguments = @($command, '-config', $savedConfig, '-dir', $runPath, '-question', $savedQuestion, '-out', $draftPath)
if ($sourcePrior) { $arguments += @('-draft', $priorPath) }
if ($sourceFeedback) { $arguments += @('-feedback', $feedbackCopy) }
$started = [DateTime]::UtcNow
$processExit = -1
$invocationError = $null
Push-Location -LiteralPath $runPath
try {
    & $savedExe @arguments 1> $stdoutPath 2> $stderrPath
    $processExit = $LASTEXITCODE
}
catch {
    $invocationError = $_.Exception.Message
}
finally {
    Pop-Location
    $finished = [DateTime]::UtcNow
    $receipt = [ordered]@{
        version='0.0.8'; operation=$command; stage=$Stage; arguments=$arguments;
        executable=$savedExe; config=$savedConfig; question=$savedQuestion; draft=$draftPath;
        prior_draft=$(if ($sourcePrior) { $priorPath } else { $null });
        feedback=$(if ($sourceFeedback) { $feedbackCopy } else { $null });
        started_utc=$started.ToString('o'); finished_utc=$finished.ToString('o');
        elapsed_seconds=($finished-$started).TotalSeconds; exit_code=$processExit; invocation_error=$invocationError;
        stdout=$stdoutPath; stderr=$stderrPath; debug=(Join-Path $runPath $settings.files.debug);
        confirmation_requested=$false; hive_execution_requested=$false
    }
    $stream = [IO.File]::Open($receiptPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try {
        $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($receipt | ConvertTo-Json -Depth 6))
        $stream.Write($bytes, 0, $bytes.Length)
    }
    finally { $stream.Dispose() }
}
[ordered]@{exit_code=$processExit;draft=$draftPath;receipt=$receiptPath;stdout=$stdoutPath;stderr=$stderrPath} | ConvertTo-Json -Compress
if ($invocationError) { [Console]::Error.WriteLine($invocationError) }
if ($processExit -ne 0 -and (Test-Path -LiteralPath $stderrPath -PathType Leaf)) { [Console]::Error.Write([IO.File]::ReadAllText($stderrPath)) }
if ($processExit -eq 0) {
    Write-Output "Proposal saved for review: $draftPath. No hive or campaign was started."
    if ($Kind -eq 'meta') { Write-Output 'After confirmation, start the meta experiment or campaign in a fresh execution folder and pass this saved proposal by path.' }
}
exit $processExit
