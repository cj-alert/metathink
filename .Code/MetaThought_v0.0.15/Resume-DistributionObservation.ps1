#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [ValidateRange(1,2147483647)][int]$ObservationSeconds = 300,
    [switch]$AllowFailed
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$runItem = Get-Item -LiteralPath $RunFolder
if (-not $runItem.PSIsContainer) { throw 'RunFolder must be an existing saved experiment directory.' }
$runPath = $runItem.FullName
$resumeScript = Join-Path $PSScriptRoot 'Resume-WorkerExperiment.ps1'
$pauseScript = Join-Path $PSScriptRoot 'Run-DistributionObservation.ps1'
$pwshExe = Join-Path $PSHOME 'pwsh.exe'
foreach ($required in @($resumeScript,$pauseScript,$pwshExe,(Join-Path $runPath 'MetaThought.exe'),(Join-Path $runPath 'config.json'))) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) { throw "Missing required file: $required" }
}

$started = [DateTime]::UtcNow
$stamp = $started.ToString('yyyyMMddTHHmmss.fffffffZ')
$receiptPath = Join-Path $runPath "observation-resume-$stamp.receipt.json"
# Reserve this attempt before starting either process; never replace an earlier receipt.
$receiptStream = [IO.File]::Open($receiptPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write)
$timer = $null
$runExit = -1
$invocationError = $null
$timerStopped = $false
try {
    # The timer only requests native pause; the saved hive owns draining and persistence.
    $timerArguments = @('-NoLogo','-NoProfile','-NonInteractive','-File',('"' + $pauseScript + '"'),'-RunFolder',('"' + $runPath + '"'),'-ObservationSeconds',[string]$ObservationSeconds,'-PauseAfterDelay')
    $timer = Start-Process -FilePath $pwshExe -ArgumentList $timerArguments -WindowStyle Hidden -PassThru
    & $resumeScript -RunFolder $runPath -Stage Run -AllowFailed:$AllowFailed
    $runExit = $LASTEXITCODE
}
catch {
    $invocationError = $_.Exception.Message
}
finally {
    try {
        if ($null -ne $timer) {
            $timer.Refresh()
            if (-not $timer.HasExited) {
                try { $timer.Kill() }
                catch { if (-not $timer.HasExited) { throw } }
                $timer.WaitForExit()
            }
            $timerStopped = $timer.HasExited
        }
        else { $timerStopped = $true }
    }
    catch {
        $invocationError = (@($invocationError,$_.Exception.Message) | Where-Object { $_ }) -join '; '
        if ($runExit -eq 0) { $runExit = -1 }
    }
    finally {
        $finished = [DateTime]::UtcNow
        $receipt = [ordered]@{
            operation='bounded resumed distribution observation';run_folder=$runPath;
            resume_script=$resumeScript;resume_stage='Run';pause_script=$pauseScript;allow_failed=[bool]$AllowFailed;
            started_utc=$started.ToString('o');finished_utc=$finished.ToString('o');
            elapsed_seconds=($finished-$started).TotalSeconds;observation_seconds=$ObservationSeconds;
            run_exit=$runExit;timer_stopped=$timerStopped;invocation_error=$invocationError
        }
        try {
            $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($receipt | ConvertTo-Json))
            $receiptStream.Write($bytes,0,$bytes.Length)
        }
        finally {
            $receiptStream.Dispose()
            if ($null -ne $timer) { $timer.Dispose() }
        }
    }
}
if ($invocationError) { [Console]::Error.WriteLine($invocationError) }
[ordered]@{run_folder=$runPath;run_exit=$runExit;controller_receipt=$receiptPath;timer_stopped=$timerStopped} | ConvertTo-Json -Compress
exit $runExit
