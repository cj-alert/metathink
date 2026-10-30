#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [string]$CheckpointFolder,
    [ValidateSet('Prepare','Run','PrepareAndRun')][string]$Stage = 'PrepareAndRun'
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$runPath = [IO.Path]::GetFullPath($RunFolder)
$runConfig = Join-Path $runPath 'config.json'
$runExe = Join-Path $runPath 'MetaThought.exe'
$runHandoff = Join-Path $runPath 'handoff.md'
$utf8 = [Text.UTF8Encoding]::new($false)

function Write-NewBytes([string]$Path, [byte[]]$Bytes) {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try { $stream.Write($Bytes, 0, $Bytes.Length) }
    finally { $stream.Dispose() }
}

if ($Stage -eq 'Run' -and $PSBoundParameters.ContainsKey('CheckpointFolder')) {
    throw 'Run uses the prepared files only; CheckpointFolder applies to preparation.'
}
if ($Stage -ne 'Run') {
    if (Test-Path -LiteralPath $runPath) { throw 'Preserve the existing folder. Prepare requires a fresh RunFolder; use -Stage Run for a prepared, unstarted inquiry.' }
    if (-not $CheckpointFolder) {
        $projectPath = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
        $CheckpointFolder = Join-Path $projectPath '.Experiments/MetaThought_v0.0.8f - Frozen Queen handoff'
    }
    $checkpointPath = [IO.Path]::GetFullPath($CheckpointFolder)
    $sourceConfig = Join-Path $PSScriptRoot 'config.json'
    $sourceExe = Join-Path $PSScriptRoot 'MetaThought.exe'
    $unchanged = @('question.txt','intake.md','queen-intake-role.txt')
    $changed = @('handoff.md','queen-handoff-role.txt')
    $sources = @($sourceConfig, $sourceExe)
    foreach ($name in ($unchanged + $changed)) { $sources += Join-Path $checkpointPath $name }
    foreach ($path in $sources) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required preparation input is missing: $path" }
    }

    [IO.Directory]::CreateDirectory($runPath) | Out-Null
    [IO.File]::Copy($sourceConfig, $runConfig, $false)
    [IO.File]::Copy($sourceExe, $runExe, $false)
    foreach ($name in $unchanged) {
        [IO.File]::Copy((Join-Path $checkpointPath $name), (Join-Path $runPath $name), $false)
    }
    $replacements = [ordered]@{}
    $phrase = [regex]::new([regex]::Escape('two drones'), [Text.RegularExpressions.RegexOptions]::IgnoreCase -bor [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    foreach ($name in $changed) {
        $source = Join-Path $checkpointPath $name
        [IO.File]::Copy($source, (Join-Path $runPath "original-$name"), $false)
        # Latin1 transports bytes one-to-one. The literal ASCII replacement
        # preserves every other byte, including original BOMs and line endings.
        $text = [Text.Encoding]::Latin1.GetString([IO.File]::ReadAllBytes($source))
        $replacements[$name] = $phrase.Matches($text).Count
        $modified = $phrase.Replace($text, 'multiple drones')
        Write-NewBytes (Join-Path $runPath $name) ([Text.Encoding]::Latin1.GetBytes($modified))
    }
    $preparation = [ordered]@{
        version='0.0.9'; operation='prepare inquiry'; prepared_utc=[DateTime]::UtcNow.ToString('o');
        checkpoint=$checkpointPath; config_source=$sourceConfig; executable_source=$sourceExe;
        unchanged_files=$unchanged; original_files=@('original-handoff.md','original-queen-handoff-role.txt');
        transformation=[ordered]@{from='two drones';to='multiple drones';case_insensitive=$true;replacements=$replacements};
        provider_calls=0; copied_prior_databases=$false; copied_prior_logs=$false
    }
    Write-NewBytes (Join-Path $runPath 'preparation-receipt.json') ($utf8.GetBytes(($preparation | ConvertTo-Json -Depth 6)))
}

if ($Stage -eq 'Prepare') {
    [ordered]@{stage='prepared';run_folder=$runPath;handoff=$runHandoff;receipt=(Join-Path $runPath 'preparation-receipt.json');provider_calls=0} | ConvertTo-Json -Compress
    exit 0
}
foreach ($name in @('config.json','MetaThought.exe','question.txt','intake.md','queen-intake-role.txt','handoff.md','queen-handoff-role.txt','original-handoff.md','original-queen-handoff-role.txt')) {
    if (-not (Test-Path -LiteralPath (Join-Path $runPath $name) -PathType Leaf)) { throw "Prepared inquiry file is missing: $name" }
}
$settings = [IO.File]::ReadAllText($runConfig) | ConvertFrom-Json
$stdoutPath = Join-Path $runPath 'inquiry.stdout.txt'
$stderrPath = Join-Path $runPath 'inquiry.stderr.txt'
$exitPath = Join-Path $runPath 'inquiry.exitcode.txt'
$receiptPath = Join-Path $runPath 'inquiry.receipt.json'
foreach ($path in @($stdoutPath, $stderrPath, $exitPath, $receiptPath, (Join-Path $runPath $settings.files.state), (Join-Path $runPath $settings.files.debug))) {
    if (Test-Path -LiteralPath $path) { throw "Preserve the existing inquiry attempt; this launcher never reruns it: $path" }
}
$arguments = @('inquire','-config',$runConfig,'-dir',$runPath,'-handoff',$runHandoff,'-confirm')
$started = [DateTime]::UtcNow
$processExit = -1
$invocationError = $null
Push-Location -LiteralPath $runPath
try {
    & $runExe @arguments 1> $stdoutPath 2> $stderrPath
    $processExit = $LASTEXITCODE
}
catch { $invocationError = $_.Exception.Message }
finally {
    Pop-Location
    $finished = [DateTime]::UtcNow
    Write-NewBytes $exitPath ($utf8.GetBytes([string]$processExit))
    $receipt = [ordered]@{
        version='0.0.9'; operation='inquire'; executable=$runExe; config=$runConfig; handoff=$runHandoff;
        arguments=$arguments; working_directory=$runPath;
        started_utc=$started.ToString('o'); finished_utc=$finished.ToString('o'); elapsed_seconds=($finished-$started).TotalSeconds;
        exit_code=$processExit; invocation_error=$invocationError; stdout=$stdoutPath; stderr=$stderrPath;
        state=(Join-Path $runPath $settings.files.state); debug=(Join-Path $runPath $settings.files.debug)
    }
    Write-NewBytes $receiptPath ($utf8.GetBytes(($receipt | ConvertTo-Json -Depth 6)))
}
[ordered]@{exit_code=$processExit;elapsed_seconds=$receipt.elapsed_seconds;run_folder=$runPath;stdout=$stdoutPath;stderr=$stderrPath;receipt=$receiptPath} | ConvertTo-Json -Compress
if ($invocationError) { [Console]::Error.WriteLine($invocationError) }
if ($processExit -ne 0 -and (Test-Path -LiteralPath $stderrPath -PathType Leaf)) { [Console]::Error.Write([IO.File]::ReadAllText($stderrPath)) }
exit $processExit
