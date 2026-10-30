param(
  [Parameter(Mandatory=$true)][string]$RunFolder,
  [Parameter(Mandatory=$true)][string]$Transcript,
  [Parameter(Mandatory=$true)][string]$Subject
)
$ErrorActionPreference = 'Stop'
$goExe = 'C:\Program Files\Go\bin\go.exe'
if (-not (Test-Path -LiteralPath $goExe -PathType Leaf)) { throw 'Configured Go SDK is missing.' }
$transcriptPath = [IO.Path]::GetFullPath($Transcript)
if (-not (Test-Path -LiteralPath $transcriptPath -PathType Leaf)) { throw 'Saved transcript is missing.' }
$runPath = [IO.Path]::GetFullPath($RunFolder)
if (Test-Path -LiteralPath $runPath) { throw 'Choose a new reassessment folder; existing output is preserved.' }
$null = New-Item -ItemType Directory -Path $runPath
$env:CGO_ENABLED = '0'
$env:GOTOOLCHAIN = 'local'
$env:GOFLAGS = '-buildvcs=false'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
Set-Location -LiteralPath $PSScriptRoot
$started = [DateTimeOffset]::UtcNow
& $goExe version > (Join-Path $runPath 'go-version.txt') 2>&1
if ($LASTEXITCODE -ne 0) { throw 'Go version probe failed; see go-version.txt.' }
$app = Join-Path $runPath 'MetaThought.exe'
$buildArgs = @('build', '-trimpath', '-o', $app, '.')
& $goExe @buildArgs > (Join-Path $runPath 'build.stdout.txt') 2> (Join-Path $runPath 'build.stderr.txt')
$buildExit = $LASTEXITCODE
[ordered]@{runtime=$goExe;arguments=$buildArgs;cwd=$PSScriptRoot;started_utc=$started.ToString('o');finished_utc=[DateTimeOffset]::UtcNow.ToString('o');exit_code=$buildExit} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $runPath 'build.receipt.json') -Encoding utf8NoBOM
if ($buildExit -ne 0) { exit $buildExit }
$config = Join-Path $PSScriptRoot 'config.json'
$runArgs = @('reassess', '-config', $config, '-dir', $runPath, '-request', $transcriptPath, '-subject', $Subject)
$runStarted = [DateTimeOffset]::UtcNow
& $app @runArgs > (Join-Path $runPath 'run.stdout.txt') 2> (Join-Path $runPath 'run.stderr.txt')
$runExit = $LASTEXITCODE
[ordered]@{runtime=$app;arguments=$runArgs;cwd=$PSScriptRoot;started_utc=$runStarted.ToString('o');finished_utc=[DateTimeOffset]::UtcNow.ToString('o');exit_code=$runExit;hive_rerun=$false;automatic_retry=$false} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $runPath 'run.receipt.json') -Encoding utf8NoBOM
Get-Content -LiteralPath (Join-Path $runPath 'run.stdout.txt')
if ($runExit -ne 0) { Get-Content -LiteralPath (Join-Path $runPath 'run.stderr.txt') }
exit $runExit
