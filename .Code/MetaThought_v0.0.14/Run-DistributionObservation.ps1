#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [int]$ObservationSeconds = 300,
    [switch]$PauseAfterDelay
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$runPath = (Get-Item -LiteralPath $RunFolder).FullName
$runExe = Join-Path $runPath 'MetaThought.exe'
$configPath = Join-Path $runPath 'config.json'
$controllerReceiptPath = Join-Path $runPath 'observation-controller.receipt.json'
if (-not $PauseAfterDelay -and (Test-Path -LiteralPath $controllerReceiptPath)) { throw 'Preserve the existing observation receipt; use a fresh experiment folder.' }
if ($ObservationSeconds -lt 1) { throw 'ObservationSeconds must be positive.' }
foreach ($required in @($runExe,$configPath)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) { throw "Missing prepared file: $required" }
}
if ($PauseAfterDelay) {
    Start-Sleep -Seconds $ObservationSeconds
    $stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffffffZ')
    $prefix = Join-Path $runPath "observation-pause-$stamp"
    & $runExe worker-pause -config $configPath -dir $runPath 1> "$prefix.stdout.txt" 2> "$prefix.stderr.txt"
    $pauseExit = $LASTEXITCODE
    [ordered]@{operation='external observation pause';requested_utc=[DateTime]::UtcNow.ToString('o');observation_seconds=$ObservationSeconds;exit_code=$pauseExit;run_folder=$runPath;provider_calls=0} |
        ConvertTo-Json | Set-Content -LiteralPath "$prefix.receipt.json" -Encoding utf8
    exit $pauseExit
}
# This timer is an operator test boundary; it adds no quota to the hive.
$arguments = @('-NoProfile','-File',('"' + $PSCommandPath + '"'),'-RunFolder',('"' + $runPath + '"'),'-ObservationSeconds',[string]$ObservationSeconds,'-PauseAfterDelay')
$timer = Start-Process -FilePath (Join-Path $PSHOME 'pwsh.exe') -ArgumentList $arguments -WindowStyle Hidden -PassThru
$runExit = -1
try {
    & (Join-Path $PSScriptRoot 'Run-WorkerExperiment.ps1') -RunFolder $runPath -Stage Run
    $runExit = $LASTEXITCODE
}
finally {
    $timer.Refresh()
    if (-not $timer.HasExited) { $timer.Kill(); $timer.WaitForExit() }
    $receipt = [ordered]@{operation='bounded distribution observation';finished_utc=[DateTime]::UtcNow.ToString('o');observation_seconds=$ObservationSeconds;run_exit=$runExit;timer_stopped=$timer.HasExited;run_folder=$runPath}
    $stream = [IO.File]::Open($controllerReceiptPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write)
    try { $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($receipt | ConvertTo-Json)); $stream.Write($bytes,0,$bytes.Length) }
    finally { $stream.Dispose() }
    $timer.Dispose()
}
exit $runExit
