#requires -Version 7.0
[CmdletBinding(DefaultParameterSetName = 'Work')]
param(
    [Parameter(Mandatory)]
    [ValidateNotNullOrEmpty()]
    [string]$RunFolder,

    [Parameter(ParameterSetName = 'Work')]
    [ValidateSet('Draft', 'Start')]
    [string]$Stage = 'Draft',

    [Parameter(ParameterSetName = 'Work')]
    [string]$BaselineFolder,

    [Parameter(ParameterSetName = 'Work')]
    [ValidateRange(1, 9223372036854775807)]
    [long]$BaselineRequestID = 1,

    [Parameter(ParameterSetName = 'Work')]
    [string]$MetaContract,

    [Parameter(ParameterSetName = 'Work')]
    [switch]$Confirm,

    [Parameter(ParameterSetName = 'Work')]
    [string]$ConfigPath = (Join-Path $PSScriptRoot 'config.json'),

    [Parameter(ParameterSetName = 'Work')]
    [string]$ExecutablePath = (Join-Path $PSScriptRoot 'MetaThought.exe'),

    [Parameter(Mandatory, ParameterSetName = 'Resume')]
    [switch]$Resume
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if (-not $Resume -and $Stage -eq 'Draft') {
    throw 'Meta planning moved to Run-Planning.ps1 -Kind meta -RunFolder <new-folder> -QuestionPath <exact-question-file>. Review that proposal before -Stage Start -MetaContract <reviewed-file> -Confirm.'
}
if ($PSCmdlet.ParameterSetName -eq 'Resume' -and -not $Resume) { throw 'Specify -Resume to continue the saved campaign.' }
if (-not $Resume) {
    if ($Stage -eq 'Start') {
        if (-not $MetaContract -or -not $Confirm -or -not $BaselineFolder) {
            throw 'Start requires -BaselineFolder, an explicitly reviewed -MetaContract path, and -Confirm.'
        }
        $MetaContract = [System.IO.Path]::GetFullPath($MetaContract)
        if (-not (Test-Path -LiteralPath $MetaContract -PathType Leaf)) { throw 'Reviewed meta contract file is missing.' }
    }
    elseif ($MetaContract -or $Confirm -or $BaselineFolder) {
        throw 'Draft only creates a meta contract. Use -Stage Start with the reviewed contract and confirmation to select a baseline.'
    }
}
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
    $sourceConfig = (Get-Item -LiteralPath $ConfigPath -ErrorAction Stop).FullName
    if (-not (Test-Path -LiteralPath $sourceConfig -PathType Leaf)) {
        throw "Configuration is not a file: $sourceConfig"
    }
    if ($Stage -eq 'Start') {
        $baselinePath = (Get-Item -LiteralPath $BaselineFolder -ErrorAction Stop).FullName
        if (-not (Test-Path -LiteralPath $baselinePath -PathType Container)) { throw "Baseline is not a directory: $baselinePath" }
    }
    $settings = Get-Content -LiteralPath $sourceConfig -Raw | ConvertFrom-Json
    if (Test-Path -LiteralPath $campaignPath) {
        if (-not (Test-Path -LiteralPath $campaignPath -PathType Container)) {
            throw "Campaign path is not a directory: $campaignPath"
        }
    }
    else {
        [System.IO.Directory]::CreateDirectory($campaignPath) | Out-Null
    }
    foreach ($name in @($settings.files.state, $settings.files.result, $settings.files.contract, 'config.json', 'mq_config.json', 'comparison.md')) {
        if (Test-Path -LiteralPath (Join-Path $campaignPath $name)) { throw 'Campaign artifacts already exist; use -Resume for an existing campaign or choose a new folder.' }
    }
    if ($Stage -eq 'Draft' -and (Test-Path -LiteralPath (Join-Path $campaignPath 'meta-contract.md'))) { throw 'Meta draft already exists; preserve it and use another draft folder.' }
    if (-not (Test-Path -LiteralPath $campaignExe)) {
        $sourceExe = (Get-Item -LiteralPath $ExecutablePath -ErrorAction Stop).FullName
        if (-not (Test-Path -LiteralPath $sourceExe -PathType Leaf)) { throw "Executable is not a file: $sourceExe" }
        [System.IO.File]::Copy($sourceExe, $campaignExe, $false)
    }
    elseif (-not (Test-Path -LiteralPath $campaignExe -PathType Leaf)) { throw 'Saved campaign executable is not a file.' }
    # Drafting writes no active configuration. RunEvolution owns the start snapshot.
    $commandName = 'meta-plan'
    $arguments = @($commandName, '-config', $sourceConfig, '-dir', $campaignPath)
    if ($Stage -eq 'Start') {
        $commandName = 'evolve'
        $arguments = @($commandName, '-config', $sourceConfig, '-dir', $campaignPath,
            '-baseline', $baselinePath, '-baseline-request', [string]$BaselineRequestID,
            '-meta-contract', $MetaContract, '-confirm')
    }
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
if ($processExit -eq 0 -and $commandName -eq 'meta-plan') { Write-Output 'Stopped after meta drafting. Review meta-contract.md; no campaign has started.' }
exit $processExit
