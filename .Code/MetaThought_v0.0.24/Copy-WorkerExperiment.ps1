#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$SourceFolder,
    [Parameter(Mandatory)][string]$RunFolder
)

$ErrorActionPreference = 'Stop'
$sourcePath = (Resolve-Path -LiteralPath $SourceFolder).ProviderPath
$sourcePath = [IO.Path]::GetFullPath($sourcePath).TrimEnd([IO.Path]::DirectorySeparatorChar)
$runPath = [IO.Path]::GetFullPath($RunFolder).TrimEnd([IO.Path]::DirectorySeparatorChar)
$sourceExe = Join-Path $sourcePath 'MetaThought.exe'
$sourceConfig = Join-Path $sourcePath 'config.json'
$utf8 = [Text.UTF8Encoding]::new($false)
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ')

function Write-NewBytes([string]$Path, [byte[]]$Bytes) {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try { $stream.Write($Bytes, 0, $Bytes.Length) }
    finally { $stream.Dispose() }
}

function Assert-SourceStopped {
    $processes = @(Get-CimInstance -ClassName Win32_Process -Filter "Name = 'MetaThought.exe'")
    foreach ($process in $processes) {
        if (-not $process.ExecutablePath) {
            throw "Cannot establish the executable path of MetaThought PID $($process.ProcessId); source stop cannot be established."
        }
        if ([string]::Equals([IO.Path]::GetFullPath($process.ExecutablePath), $sourceExe, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Source executable is active (PID $($process.ProcessId)). Pause it and wait for its saved result before copying."
        }
    }
}

function Write-CopyEvent([string]$Event, [string]$Source, [string]$Destination, [long]$Bytes) {
    $entry = [ordered]@{
        ts = [DateTime]::UtcNow.ToString('o'); event = $Event
        source = $Source; destination = $Destination; bytes = $Bytes
    }
    [IO.File]::AppendAllText($eventsPath, ($entry | ConvertTo-Json -Compress) + [Environment]::NewLine, $utf8)
}

if (-not (Test-Path -LiteralPath $sourcePath -PathType Container)) { throw 'SourceFolder must be an existing experiment folder.' }
if ([string]::Equals($sourcePath, $runPath, [StringComparison]::OrdinalIgnoreCase) -or
    $runPath.StartsWith($sourcePath + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'The new run must be a distinct folder outside the source experiment.'
}
if (Test-Path -LiteralPath $runPath) { throw 'RunFolder already exists. Preserve it and use a fresh folder name.' }
$items = @(Get-ChildItem -LiteralPath $sourcePath -Force)
if (@($items | Where-Object PSIsContainer).Count -ne 0) {
    throw 'Source experiment contains a subfolder. This process copies flat experiment folders only.'
}
$files = @($items | Where-Object { -not $_.PSIsContainer } | Sort-Object Name)
foreach ($path in @($sourceExe, $sourceConfig)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required saved runtime file is missing: $path" }
}
Assert-SourceStopped
$latest = $files | Where-Object Name -Like 'worker-result-*.json' | Sort-Object Name -Descending | Select-Object -First 1
if (-not $latest) { throw 'Source has no saved worker result; do not infer a completed stop from a missing process alone.' }
$result = [IO.File]::ReadAllText($latest.FullName) | ConvertFrom-Json -AsHashtable -Depth 100
$status = [string]$result.status
$stoppedStatuses = @('paused','observation_paused','observed_growth','convergence_requested','idle','failed','blocked','returned','finished','contract_revised')
if ($status -notin $stoppedStatuses) { throw "Saved worker status '$status' is not an explicit stopped result. Preserve and review it before copying." }
$settings = [IO.File]::ReadAllText($sourceConfig) | ConvertFrom-Json -AsHashtable -Depth 100
if ($settings -isnot [System.Collections.IDictionary] -or $settings.files -isnot [System.Collections.IDictionary] -or
    $settings.dashboard -isnot [System.Collections.IDictionary]) {
    throw 'Saved configuration must contain files and dashboard objects.'
}
foreach ($field in @('state','debug')) {
    $name = [string]$settings.files[$field]
    if ([string]::IsNullOrWhiteSpace($name) -or [IO.Path]::IsPathRooted($name) -or
        [IO.Path]::GetFileName($name) -ne $name -or $name -in @('.','..') -or
        $name.IndexOfAny([IO.Path]::GetInvalidFileNameChars()) -ge 0) {
        throw "Saved files.$field must name a flat runtime file."
    }
    if (-not (Test-Path -LiteralPath (Join-Path $sourcePath $name) -PathType Leaf)) { throw "Required saved database is missing: $name" }
}

$eventsName = "clone-preparation-$stamp.jsonl"
$receiptName = "clone-preparation-$stamp.receipt.json"
$originalConfigName = "config.before-clone-$stamp.json"
foreach ($name in @($eventsName, $receiptName, $originalConfigName)) {
    if ($name -in @($files.Name)) { throw "Preserve existing source artifact '$name'; prepare the copy with a new invocation." }
}
$eventsPath = Join-Path $runPath $eventsName
$receiptPath = Join-Path $runPath $receiptName
$preservedConfig = Join-Path $runPath $originalConfigName
$copiedConfig = Join-Path $runPath 'config.json'
$instanceName = [IO.Path]::GetFileName($runPath)
$priorInstanceName = $settings.dashboard.instance_name
$started = [DateTime]::UtcNow
$copied = 0
$prepared = $false
$failure = $null

# Copy opaque bytes only. No database connection, checkpoint, claim recovery,
# model call or executable launch is part of preparation. SQLite sidecars, if
# present among the stopped source's files, are copied with all other history.
[IO.Directory]::CreateDirectory($runPath) | Out-Null
Write-NewBytes $eventsPath ([byte[]]::new(0))
try {
    Assert-SourceStopped
    foreach ($file in $files) {
        $target = Join-Path $runPath $file.Name
        [IO.File]::Copy($file.FullName, $target, $false)
        $copied++
        Write-CopyEvent 'file.copy' $file.FullName $target $file.Length
    }
    Assert-SourceStopped
    [IO.File]::Copy($copiedConfig, $preservedConfig, $false)
    Write-CopyEvent 'file.preserve_config' $copiedConfig $preservedConfig ([IO.FileInfo]::new($preservedConfig).Length)
    # This is the sole configuration change. The byte-exact copied config is
    # preserved beside it; every other parsed value stays as saved in source.
    $settings.dashboard.instance_name = $instanceName
    [IO.File]::WriteAllText($copiedConfig, ($settings | ConvertTo-Json -Depth 100), $utf8)
    Write-CopyEvent 'config.set_dashboard_instance' $preservedConfig $copiedConfig ([IO.FileInfo]::new($copiedConfig).Length)
    $prepared = $true
}
catch {
    $failure = $_.Exception.Message
    throw
}
finally {
    $finished = [DateTime]::UtcNow
    $receipt = [ordered]@{
        operation = 'copy stopped worker experiment'; status = $(if ($prepared) { 'prepared' } else { 'failed' })
        source_folder = $sourcePath; run_folder = $runPath
        source_executable = $sourceExe; saved_result = $latest.FullName; saved_status = $status
        started_utc = $started.ToString('o'); finished_utc = $finished.ToString('o')
        elapsed_seconds = ($finished - $started).TotalSeconds
        source_file_count = $files.Count; copied_file_count = $copied
        events = $eventsPath; preserved_config = $preservedConfig; config = $copiedConfig
        changed_config_fields = @('dashboard.instance_name')
        previous_instance_name = $priorInstanceName; instance_name = $instanceName
        state = (Join-Path $runPath $settings.files.state); debug = (Join-Path $runPath $settings.files.debug)
        sqlite_sidecars_copied = @($files.Name | Where-Object { $_ -match '\.(db|sqlite|sqlite3)-(wal|shm|journal)$' })
        provider_calls = 0; runtime_started = $false; claims_released = $false; error = $failure
    }
    Write-NewBytes $receiptPath ($utf8.GetBytes(($receipt | ConvertTo-Json -Depth 12)))
}
[ordered]@{status='prepared';source_folder=$sourcePath;run_folder=$runPath;copied_files=$copied;instance_name=$instanceName;receipt=$receiptPath;events=$eventsPath;provider_calls=0;runtime_started=$false} | ConvertTo-Json -Compress
