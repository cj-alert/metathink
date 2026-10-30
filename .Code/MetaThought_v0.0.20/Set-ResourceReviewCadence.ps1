#requires -Version 7.5
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [Parameter(Mandatory)][ValidateRange(0, [int]::MaxValue)][int]$Every,
    [string]$BackupScript
)

$ErrorActionPreference = 'Stop'
$utf8 = [Text.UTF8Encoding]::new($false)
$runItem = Get-Item -LiteralPath $RunFolder -ErrorAction Stop
if (-not $runItem.PSIsContainer) { throw 'RunFolder must be an existing directory.' }
$runPath = $runItem.FullName
$configPath = Join-Path $runPath 'config.json'
$runExe = Join-Path $runPath 'MetaThought.exe'
foreach ($path in @($configPath, $runExe)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Saved runtime file is missing: $path" }
}
if ([string]::IsNullOrWhiteSpace($BackupScript)) {
    $projectsParent = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
    $BackupScript = Join-Path $projectsParent '!GIT_FounderOperatingSystem/!codex_skills/founderos-shell-reliability/scripts/New-VerifiedLocalBackup.ps1'
}
if (-not (Test-Path -LiteralPath $BackupScript -PathType Leaf)) { throw "Canonical backup helper missing: $BackupScript" }

function Assert-RunStopped {
    foreach ($process in @(Get-CimInstance -ClassName Win32_Process -Filter "Name = 'MetaThought.exe'")) {
        if (-not $process.ExecutablePath) { throw "Cannot establish the executable path of MetaThought process $($process.ProcessId); configuration was not changed." }
        if ([string]::Equals([IO.Path]::GetFullPath($process.ExecutablePath), $runExe, [StringComparison]::OrdinalIgnoreCase)) {
            throw "This run executable is active (PID $($process.ProcessId)); pause and drain it before changing resource-review cadence."
        }
    }
}

function Read-CadenceConfig {
    $settings = [IO.File]::ReadAllText($configPath) | ConvertFrom-Json -AsHashtable -Depth 100 -DateKind String
    if ($settings -isnot [Collections.IDictionary] -or $settings.workers -isnot [Collections.IDictionary] -or
        -not $settings.workers.Contains('resource_review_every') -or
        ($settings.workers.resource_review_every -isnot [int] -and $settings.workers.resource_review_every -isnot [long]) -or
        $settings.workers.resource_review_every -lt 0) {
        throw 'Configuration must contain a nonnegative integer workers.resource_review_every.'
    }
    return $settings
}

Assert-RunStopped
$settings = Read-CadenceConfig
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ')
$receiptPath = Join-Path $runPath "resource-cadence-$stamp.receipt.json"
if (Test-Path -LiteralPath $receiptPath) { throw "Preserve existing receipt: $receiptPath" }
$receipt = [ordered]@{
    operation='set stopped worker resource-review cadence';run_folder=$runPath;config=$configPath;
    started_utc=[DateTime]::UtcNow.ToString('o');finished_utc=$null;
    changed_fields=@('workers.resource_review_every');before=$settings.workers.resource_review_every;
    requested=$Every;after=$null;backup=$null;status='started';error=$null;provider_calls=0
}
$failure = $null
try {
    if ($settings.workers.resource_review_every -eq $Every) {
        $receipt.after = $settings.workers.resource_review_every
        $receipt.status = 'unchanged'
    } else {
        $backup = (& $BackupScript -SourcePath $configPath -AsJson) | ConvertFrom-Json -AsHashtable
        # config.json is not an excluded binary or append-only artifact.
        if ($backup.Status -cne 'BackedUp' -or -not (Test-Path -LiteralPath $backup.BackupPath -PathType Leaf)) {
            throw 'Canonical configuration backup was not confirmed.'
        }
        $receipt.backup = $backup.BackupPath
        Assert-RunStopped
        $settings.workers.resource_review_every = $Every
        [IO.File]::WriteAllText($configPath, ($settings | ConvertTo-Json -Depth 100), $utf8)
        $readback = Read-CadenceConfig
        $receipt.after = $readback.workers.resource_review_every
        if ($readback.workers.resource_review_every -ne $Every) { throw 'Saved resource-review cadence differs from the requested value.' }
        $receipt.status = 'updated'
    }
}
catch {
    $failure = $_
    $receipt.status = 'failed'
    $receipt.error = $_.Exception.Message
}
finally {
    $receipt.finished_utc = [DateTime]::UtcNow.ToString('o')
    $stream = [IO.File]::Open($receiptPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try {
        $bytes = $utf8.GetBytes(($receipt | ConvertTo-Json -Depth 8))
        $stream.Write($bytes, 0, $bytes.Length)
    }
    finally { $stream.Dispose() }
}
[ordered]@{status=$receipt.status;config=$configPath;before=$receipt.before;after=$receipt.after;backup=$receipt.backup;receipt=$receiptPath;provider_calls=0} | ConvertTo-Json -Compress
if ($failure) { throw $failure }
