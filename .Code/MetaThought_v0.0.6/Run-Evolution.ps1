#requires -Version 7.0
[CmdletBinding(DefaultParameterSetName = 'Start')]
param(
    [Parameter(Mandatory)]
    [ValidateNotNullOrEmpty()]
    [string]$RunFolder,

    [Parameter(Mandatory, ParameterSetName = 'Start')]
    [ValidateNotNullOrEmpty()]
    [string]$BaselineFolder,

    [Parameter(ParameterSetName = 'Start')]
    [ValidateRange(1, 9223372036854775807)]
    [long]$BaselineRequestID = 1,

    [Parameter(ParameterSetName = 'Start')]
    [string]$ConfigPath = (Join-Path $PSScriptRoot 'config.json'),

    [Parameter(ParameterSetName = 'Start')]
    [string]$ExecutablePath = (Join-Path $PSScriptRoot 'MetaThought.exe'),

    [Parameter(Mandatory, ParameterSetName = 'Resume')]
    [switch]$Resume
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$campaignPath = [System.IO.Path]::GetFullPath($RunFolder)
$campaignExe = Join-Path $campaignPath 'MetaThought.exe'

if ($Resume) {
    if (-not (Test-Path -LiteralPath $campaignPath -PathType Container)) {
        throw "Campaign folder does not exist: $campaignPath"
    }
    if (-not (Test-Path -LiteralPath $campaignExe -PathType Leaf)) {
        throw "Saved campaign executable does not exist: $campaignExe"
    }
    $savedConfig = Join-Path $campaignPath 'config.json'
    if (-not (Test-Path -LiteralPath $savedConfig -PathType Leaf)) {
        throw "Saved campaign configuration does not exist: $savedConfig"
    }
    $commandName = 'evolve-resume'
    $arguments = @($commandName, '-dir', $campaignPath)
}
else {
    $sourceExe = (Get-Item -LiteralPath $ExecutablePath -ErrorAction Stop).FullName
    $sourceConfig = (Get-Item -LiteralPath $ConfigPath -ErrorAction Stop).FullName
    $baselinePath = (Get-Item -LiteralPath $BaselineFolder -ErrorAction Stop).FullName
    if (-not (Test-Path -LiteralPath $sourceExe -PathType Leaf)) {
        throw "Executable is not a file: $sourceExe"
    }
    if (-not (Test-Path -LiteralPath $sourceConfig -PathType Leaf)) {
        throw "Configuration is not a file: $sourceConfig"
    }
    if (-not (Test-Path -LiteralPath $baselinePath -PathType Container)) {
        throw "Baseline is not a directory: $baselinePath"
    }
    if (Test-Path -LiteralPath $campaignPath) {
        if (-not (Test-Path -LiteralPath $campaignPath -PathType Container)) {
            throw "Campaign path is not a directory: $campaignPath"
        }
        if (@(Get-ChildItem -LiteralPath $campaignPath -Force).Count -ne 0) {
            throw "Start requires a fresh campaign folder: $campaignPath"
        }
    }
    else {
        [System.IO.Directory]::CreateDirectory($campaignPath) | Out-Null
    }
    [System.IO.File]::Copy($sourceExe, $campaignExe, $false)
    # RunEvolution owns the exclusive config.json snapshot in the campaign.
    $commandName = 'evolve'
    $arguments = @($commandName, '-config', $sourceConfig, '-dir', $campaignPath,
        '-baseline', $baselinePath, '-baseline-request', [string]$BaselineRequestID)
}

$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffffffZ')
$stdoutPath = Join-Path $campaignPath "$commandName-$stamp.stdout.txt"
$stderrPath = Join-Path $campaignPath "$commandName-$stamp.stderr.txt"
$exitPath = Join-Path $campaignPath "$commandName-$stamp.exitcode.txt"
foreach ($path in @($stdoutPath, $stderrPath, $exitPath)) {
    if (Test-Path -LiteralPath $path) {
        throw "Invocation output already exists: $path"
    }
}

Push-Location -LiteralPath $campaignPath
try {
    & $campaignExe @arguments 1> $stdoutPath 2> $stderrPath
    $processExit = $LASTEXITCODE
}
finally {
    Pop-Location
}
[System.IO.File]::WriteAllText($exitPath, [string]$processExit, [System.Text.UTF8Encoding]::new($false))
[Console]::Out.Write([System.IO.File]::ReadAllText($stdoutPath))
[Console]::Error.Write([System.IO.File]::ReadAllText($stderrPath))
exit $processExit
