#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [ValidateSet('Update','Run','UpdateAndRun')][string]$Stage = 'Update',
    [string]$SourceFolder = $PSScriptRoot,
    [switch]$AllowFailed
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$runPath = [IO.Path]::GetFullPath($RunFolder)
$sourcePath = [IO.Path]::GetFullPath($SourceFolder)
$runConfig = Join-Path $runPath 'config.json'
$runExe = Join-Path $runPath 'MetaThought.exe'
$utf8 = [Text.UTF8Encoding]::new($false)
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ')

function Write-NewBytes([string]$Path, [byte[]]$Bytes) {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try { $stream.Write($Bytes, 0, $Bytes.Length) }
    finally { $stream.Dispose() }
}

function Assert-RunStopped {
    $processes = @(Get-CimInstance -ClassName Win32_Process -Filter "Name = 'MetaThought.exe'")
    foreach ($process in $processes) {
        if (-not $process.ExecutablePath) { throw "Cannot establish the executable path of MetaThought process $($process.ProcessId); no runtime replacement or continuation was performed." }
        if ([string]::Equals([IO.Path]::GetFullPath($process.ExecutablePath), $runExe, [StringComparison]::OrdinalIgnoreCase)) {
            throw "This run executable is active (PID $($process.ProcessId)); request pause and wait for its saved result before continuing."
        }
    }
}

foreach ($path in @($runConfig, $runExe)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Saved runtime file is missing: $path" }
}
Assert-RunStopped
$latest = Get-ChildItem -LiteralPath $runPath -File -Filter 'worker-result-*.json' | Sort-Object Name -Descending | Select-Object -First 1
if (-not $latest) { throw 'A saved worker result is required; this launcher does not start a new inquiry.' }
$priorResult = [IO.File]::ReadAllText($latest.FullName) | ConvertFrom-Json -AsHashtable
$priorStatus = [string]$priorResult.status
$resumable = @('paused','observation_paused','observed_growth','convergence_requested','provider_unavailable')
if ($priorStatus -eq 'provider_unavailable' -and $priorResult.ready_for_resume -ne $true) {
    throw 'Provider stop is not marked ready_for_resume; inspect its saved diagnostics before continuing.'
}
# Earlier convergence failures saved an empty status. Recover only with the
# explicit failed-attempt switch and a matching failed continuation receipt.
if (-not $priorStatus -and $AllowFailed) {
    $matchingFailure = $false
    foreach ($savedReceipt in @(Get-ChildItem -LiteralPath $runPath -File -Filter 'workers-continue-*.receipt.json')) {
        $attempt = [IO.File]::ReadAllText($savedReceipt.FullName) | ConvertFrom-Json -AsHashtable
        if ($attempt.ContainsKey('exit_code') -and [int]$attempt.exit_code -ne 0 -and $latest.FullName -in @($attempt.results)) {
            $matchingFailure = $true
            break
        }
    }
    if (-not $matchingFailure) { throw 'An empty saved status requires its matching failed continuation receipt; no update or run was performed.' }
    $priorStatus = 'failed'
}
if ($priorStatus -eq 'failed') {
    if (-not $AllowFailed) { throw 'The last attempt failed. Review its preserved diagnostics and explicitly pass -AllowFailed for a known failure. This launcher does not release uncertain worker claims.' }
} elseif ($priorStatus -notin $resumable) {
    throw "Saved status '$priorStatus' is not a stopped, resumable worker attempt."
}
$settings = [IO.File]::ReadAllText($runConfig) | ConvertFrom-Json -AsHashtable -Depth 100
$stateName = [string]$settings.files.state
if ([IO.Path]::GetFileName($stateName) -ne $stateName -or -not (Test-Path -LiteralPath (Join-Path $runPath $stateName) -PathType Leaf)) {
    throw 'The saved configuration must point to its existing flat-folder state database.'
}
$updateReceiptPath = $null
if ($Stage -ne 'Run') {
    $sourceConfig = Join-Path $sourcePath 'config.json'
    $sourceExe = Join-Path $sourcePath 'MetaThought.exe'
    if ([string]::Equals($sourcePath, $runPath, [StringComparison]::OrdinalIgnoreCase)) { throw 'Runtime update source must be the built code folder, not this run folder.' }
    foreach ($path in @($sourceConfig, $sourceExe)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Built source file is missing: $path" }
    }
    $source = [IO.File]::ReadAllText($sourceConfig) | ConvertFrom-Json -AsHashtable -Depth 100
    if (-not $source.prompts -or -not $source.ContainsKey('dashboard') -or -not $source.workers.ContainsKey('observe')) {
        throw 'Source configuration must contain prompts, dashboard and workers.observe.'
    }
    $configBackup = Join-Path $runPath "config.before-$stamp.json"
    $exeBackup = Join-Path $runPath "MetaThought.before-$stamp.exe"
    [IO.File]::Copy($runConfig, $configBackup, $false)
    [IO.File]::Copy($runExe, $exeBackup, $false)
    # Explicit update surface: all other saved runtime settings and credentials,
    # the exact handoff, operational state and previous outputs remain intact.
    $settings.version = $source.version
    $settings.prompts = $source.prompts
    $settings.dashboard = $source.dashboard
    $settings.workers.observe = $source.workers.observe
    if ($source.meta.ContainsKey('http_500_retry')) { $settings.meta.http_500_retry = $source.meta.http_500_retry }
    if ($source.ContainsKey('alerts')) { $settings.alerts = $source.alerts }
    $updatedConfig = $utf8.GetBytes(($settings | ConvertTo-Json -Depth 100))
    Assert-RunStopped
    [IO.File]::Copy($sourceExe, $runExe, $true)
    [IO.File]::WriteAllBytes($runConfig, $updatedConfig)
    $updateReceiptPath = Join-Path $runPath "worker-update-$stamp.receipt.json"
    $update = [ordered]@{
        operation='update stopped worker runtime'; updated_utc=[DateTime]::UtcNow.ToString('o');
        run_folder=$runPath; prior_result=$latest.FullName; prior_status=$priorStatus;
        source_executable=$sourceExe; source_config=$sourceConfig; executable=$runExe; config=$runConfig;
        preserved_executable=$exeBackup; preserved_config=$configBackup;
        updated_config_fields=@('version','prompts','dashboard','workers.observe','meta.http_500_retry','alerts'); provider_calls=0
    }
    Write-NewBytes $updateReceiptPath ($utf8.GetBytes(($update | ConvertTo-Json -Depth 8)))
}
if ($Stage -eq 'Update') {
    [ordered]@{stage='updated';run_folder=$runPath;receipt=$updateReceiptPath;provider_calls=0} | ConvertTo-Json -Compress
    exit 0
}

Assert-RunStopped
$prefix = "workers-continue-$stamp"
$stdoutPath = Join-Path $runPath "$prefix.stdout.txt"
$stderrPath = Join-Path $runPath "$prefix.stderr.txt"
$exitPath = Join-Path $runPath "$prefix.exitcode.txt"
$receiptPath = Join-Path $runPath "$prefix.receipt.json"
foreach ($path in @($stdoutPath,$stderrPath,$exitPath,$receiptPath)) {
    if (Test-Path -LiteralPath $path) { throw "Preserve the existing continuation artifact: $path" }
}
$existingResults = @(Get-ChildItem -LiteralPath $runPath -File -Filter 'worker-result-*.json' | Select-Object -ExpandProperty FullName)
$arguments = @('work','-dir',$runPath)
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
    $results = @(Get-ChildItem -LiteralPath $runPath -File -Filter 'worker-result-*.json' | Where-Object { $_.FullName -notin $existingResults } | Sort-Object Name | Select-Object -ExpandProperty FullName)
    $receipt = [ordered]@{
        operation='continue saved worker experiment'; executable=$runExe; config=$runConfig; arguments=$arguments;
        working_directory=$runPath; prior_result=$latest.FullName; prior_status=$priorStatus; allow_failed=[bool]$AllowFailed;
        runtime_update_receipt=$updateReceiptPath; started_utc=$started.ToString('o'); finished_utc=$finished.ToString('o');
        elapsed_seconds=($finished-$started).TotalSeconds; exit_code=$processExit; invocation_error=$invocationError;
        stdout=$stdoutPath; stderr=$stderrPath; results=$results;
        state=(Join-Path $runPath $settings.files.state); debug=(Join-Path $runPath $settings.files.debug)
    }
    Write-NewBytes $receiptPath ($utf8.GetBytes(($receipt | ConvertTo-Json -Depth 8)))
}
if ($results.Count -gt 0) {
    & (Join-Path $PSScriptRoot 'Send-WorkerAlert.ps1') -RunFolder $runPath -ResultPath $results[-1] -ReceiptPath (Join-Path $runPath "$prefix.sms.receipt.json")
}
[ordered]@{exit_code=$processExit;elapsed_seconds=$receipt.elapsed_seconds;run_folder=$runPath;stdout=$stdoutPath;stderr=$stderrPath;receipt=$receiptPath;results=$results} | ConvertTo-Json -Compress
if ($invocationError) { [Console]::Error.WriteLine($invocationError) }
if ($processExit -ne 0 -and (Test-Path -LiteralPath $stderrPath -PathType Leaf)) { [Console]::Error.Write([IO.File]::ReadAllText($stderrPath)) }
exit $processExit
