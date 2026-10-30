#requires -Version 7.5
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RequestPath,
    [switch]$ValidateOnly,
    [string]$BackupScript = 'C:/Users/clyde/OneDrive/!Projects/!iQ/!Code_iQ/!GIT_FounderOperatingSystem/!codex_skills/founderos-shell-reliability/scripts/New-VerifiedLocalBackup.ps1'
)

$ErrorActionPreference = 'Stop'
$utf8 = [Text.UTF8Encoding]::new($false)
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ')
$manifestPath = (Get-Item -LiteralPath $RequestPath -ErrorAction Stop).FullName
$request = [IO.File]::ReadAllText($manifestPath) | ConvertFrom-Json -AsHashtable -Depth 100 -DateKind String
if ($request -isnot [Collections.IDictionary]) { throw 'Request must be a JSON object.' }
foreach ($key in $request.Keys) {
    if ($key -cnotin @('run_folders','worker_count')) { throw "Unknown request field: $key" }
}
if ($request.run_folders -isnot [array] -or $request.run_folders.Count -eq 0) { throw 'run_folders must be a nonempty array of folder paths.' }
if (($request.worker_count -isnot [int] -and $request.worker_count -isnot [long]) -or $request.worker_count -lt 1) {
    throw 'worker_count must be a positive integer.'
}
if (-not (Test-Path -LiteralPath $BackupScript -PathType Leaf)) { throw "Canonical backup helper missing: $BackupScript" }
$workerCount = $request.worker_count

function Assert-RunStopped([string]$Folder) {
    $executable = Join-Path $Folder 'MetaThought.exe'
    foreach ($process in @(Get-CimInstance -ClassName Win32_Process -Filter "Name = 'MetaThought.exe'")) {
        if (-not $process.ExecutablePath) { throw "Cannot establish the executable path of MetaThought process $($process.ProcessId); configuration was not changed." }
        if ([string]::Equals([IO.Path]::GetFullPath($process.ExecutablePath), $executable, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Run executable is active (PID $($process.ProcessId)): $executable. Stop and drain it before changing concurrency."
        }
    }
}

function Read-WorkerConfig([string]$Path) {
    $settings = [IO.File]::ReadAllText($Path) | ConvertFrom-Json -AsHashtable -Depth 100 -DateKind String
    if ($settings -isnot [Collections.IDictionary] -or $settings.workers -isnot [Collections.IDictionary] -or
        -not $settings.workers.Contains('count') -or
        ($settings.workers.count -isnot [int] -and $settings.workers.count -isnot [long]) -or $settings.workers.count -lt 1) {
        throw "Configuration must contain a positive integer workers.count: $Path"
    }
    return $settings
}

function Write-NewBytes([string]$Path, [byte[]]$Bytes) {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try { $stream.Write($Bytes, 0, $Bytes.Length) }
    finally { $stream.Dispose() }
}

# Validate every target before changing any of them. Relative paths are relative
# to the request file, not the invoking shell's current directory.
$targets = @()
foreach ($entry in $request.run_folders) {
    if ($entry -isnot [string] -or [string]::IsNullOrWhiteSpace($entry)) { throw 'Each run_folders entry must be a nonempty string.' }
    if (-not [IO.Path]::IsPathRooted($entry)) { $entry = Join-Path (Split-Path -Parent $manifestPath) $entry }
    $item = Get-Item -LiteralPath $entry -ErrorAction Stop
    if (-not $item.PSIsContainer) { throw "Run folder is not a directory: $entry" }
    $folder = $item.FullName
    if ($folder -in @($targets | ForEach-Object { $_.Folder })) { throw "Duplicate run folder: $folder" }
    if (-not (Test-Path -LiteralPath (Join-Path $folder 'MetaThought.exe') -PathType Leaf)) { throw "Saved run executable is missing: $folder" }
    Assert-RunStopped $folder
    $config = Join-Path $folder 'config.json'
    $settings = Read-WorkerConfig $config
    $receipt = Join-Path $folder "worker-count-$stamp.receipt.json"
    $pending = Join-Path $folder "worker-count-$stamp.pending.json"
    if ((Test-Path -LiteralPath $receipt) -or (Test-Path -LiteralPath $pending)) { throw "Preserve existing update artifacts in: $folder" }
    $targets += [pscustomobject]@{Folder=$folder;Config=$config;Before=$settings.workers.count;Receipt=$receipt;Pending=$pending}
}
if ($ValidateOnly) {
    [ordered]@{status='validated';worker_count=$workerCount;targets=@($targets | Select-Object Folder,Config,Before);changed_fields=@('workers.count');writes=0;provider_calls=0} | ConvertTo-Json -Depth 8
    exit 0
}

foreach ($target in $targets) {
    $receipt = [ordered]@{
        operation='set stopped worker execution concurrency';request=$manifestPath;run_folder=$target.Folder;config=$target.Config;
        started_utc=[DateTime]::UtcNow.ToString('o');finished_utc=$null;changed_fields=@('workers.count');
        before=$null;after=$null;requested=$workerCount;backup=$null;status='started';error=$null;provider_calls=0
    }
    $failure = $null
    try {
        Assert-RunStopped $target.Folder
        $settings = Read-WorkerConfig $target.Config
        $receipt.before = $settings.workers.count
        if ($settings.workers.count -eq $workerCount) {
            $receipt.after = $settings.workers.count
            $receipt.status = 'unchanged'
        } else {
            $backup = (& $BackupScript -SourcePath $target.Config -AsJson) | ConvertFrom-Json -AsHashtable
            if ($backup.Status -cne 'BackedUp' -or -not (Test-Path -LiteralPath $backup.BackupPath -PathType Leaf)) { throw 'Canonical configuration backup was not confirmed.' }
            $receipt.backup = $backup.BackupPath
            $settings.workers.count = $workerCount
            Write-NewBytes $target.Pending ($utf8.GetBytes(($settings | ConvertTo-Json -Depth 100)))
            Assert-RunStopped $target.Folder
            [IO.File]::Move($target.Pending, $target.Config, $true)
            $readback = Read-WorkerConfig $target.Config
            $receipt.after = $readback.workers.count
            if ($readback.workers.count -ne $workerCount) { throw 'Saved workers.count differs from requested concurrency.' }
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
        Write-NewBytes $target.Receipt ($utf8.GetBytes(($receipt | ConvertTo-Json -Depth 8)))
    }
    [ordered]@{status=$receipt.status;config=$target.Config;before=$receipt.before;after=$receipt.after;backup=$receipt.backup;receipt=$target.Receipt;provider_calls=0} | ConvertTo-Json -Compress
    if ($failure) { throw $failure }
}
