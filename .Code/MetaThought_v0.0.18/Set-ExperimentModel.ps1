#requires -Version 7.5
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string[]]$Folder,
    [Parameter(Mandatory)][ValidateNotNullOrEmpty()][string]$Model
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($Model)) { throw 'Model must be a nonempty model ID.' }
$utf8 = [Text.UTF8Encoding]::new($false)
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ')

function Assert-FolderStopped([string]$Path) {
    $executable = Join-Path $Path 'MetaThought.exe'
    foreach ($process in @(Get-CimInstance -ClassName Win32_Process -Filter "Name = 'MetaThought.exe'")) {
        if (-not $process.ExecutablePath) {
            throw "Cannot establish the executable path of MetaThought process $($process.ProcessId); no configuration update is allowed."
        }
        if ([string]::Equals([IO.Path]::GetFullPath($process.ExecutablePath), $executable, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Folder executable is active (PID $($process.ProcessId)): $executable. Pause and drain it before changing models."
        }
    }
}

function Write-NewReceipt([string]$Path, [object]$Receipt) {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try {
        $bytes = $utf8.GetBytes(($Receipt | ConvertTo-Json -Depth 12))
        $stream.Write($bytes, 0, $bytes.Length)
    }
    finally { $stream.Dispose() }
}

# The caller preserves the configuration through the canonical backup process
# before invoking this updater. Inspect every requested folder before any write.
$targets = @()
$seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach ($entry in $Folder) {
    $path = (Get-Item -LiteralPath $entry -ErrorAction Stop).FullName
    if (-not (Test-Path -LiteralPath $path -PathType Container)) { throw "Not a folder: $path" }
    if (-not $seen.Add($path)) { throw "Folder was specified more than once: $path" }
    Assert-FolderStopped $path
    $configPath = Join-Path $path 'config.json'
    $settings = [IO.File]::ReadAllText($configPath) | ConvertFrom-Json -AsHashtable -Depth 100 -DateKind String
    if ($settings.models -isnot [System.Collections.IDictionary] -or -not $settings.models.Contains('drone') -or -not $settings.models.Contains('queen')) {
        throw "Configuration must contain models.drone and models.queen: $configPath"
    }
    $receiptPath = Join-Path $path "model-update-$stamp.receipt.json"
    if (Test-Path -LiteralPath $receiptPath) { throw "Preserve existing receipt: $receiptPath" }
    $targets += [pscustomobject]@{Folder=$path;Config=$configPath;Receipt=$receiptPath}
}

foreach ($target in $targets) {
    $receipt = [ordered]@{
        operation='set stopped experiment models'; folder=$target.Folder; config=$target.Config;
        started_utc=[DateTime]::UtcNow.ToString('o'); finished_utc=$null;
        requested_model=$Model; changed_fields=@('models.drone','models.queen');
        before=$null; after=$null; status='started'; error=$null; provider_calls=0
    }
    $failure = $null
    try {
        Assert-FolderStopped $target.Folder
        # Reload immediately before editing so other saved settings are retained.
        $settings = [IO.File]::ReadAllText($target.Config) | ConvertFrom-Json -AsHashtable -Depth 100 -DateKind String
        if ($settings.models -isnot [System.Collections.IDictionary] -or -not $settings.models.Contains('drone') -or -not $settings.models.Contains('queen')) {
            throw "Configuration no longer contains both model fields: $($target.Config)"
        }
        $receipt.before = [ordered]@{drone=$settings.models.drone;queen=$settings.models.queen}
        $settings.models.drone = $Model
        $settings.models.queen = $Model
        [IO.File]::WriteAllText($target.Config, ($settings | ConvertTo-Json -Depth 100), $utf8)
        $readback = [IO.File]::ReadAllText($target.Config) | ConvertFrom-Json -AsHashtable -Depth 100 -DateKind String
        $receipt.after = [ordered]@{drone=$readback.models.drone;queen=$readback.models.queen}
        if ($readback.models.drone -cne $Model -or $readback.models.queen -cne $Model) {
            throw "Saved model IDs do not match the requested model: $($target.Config)"
        }
        $receipt.status = 'updated'
    }
    catch {
        $failure = $_
        $receipt.status = 'failed'
        $receipt.error = $_.Exception.Message
    }
    finally {
        $receipt.finished_utc = [DateTime]::UtcNow.ToString('o')
        Write-NewReceipt $target.Receipt $receipt
    }
    [ordered]@{status=$receipt.status;config=$target.Config;models=$receipt.after;receipt=$target.Receipt;provider_calls=0} | ConvertTo-Json -Compress
    if ($failure) { throw $failure }
}
