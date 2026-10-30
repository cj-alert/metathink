#requires -Version 7.0
<#
.SYNOPSIS
Publishes one saved instance's dashboard through the built native command and verifies its existing HTTP URL.
.PARAMETER RunFolder
Existing instance folder containing its operational state. Evidence is saved here as timestamped flat files.
.PARAMETER ConfigPath
Dashboard configuration, including publish_directory and the instance filename pattern. Defaults to this source folder's config.json.
.PARAMETER BaseUrl
Verified existing webserver URL corresponding to publish_directory, for example http://127.0.0.1:39119/.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [string]$ConfigPath = (Join-Path $PSScriptRoot 'config.json'),
    [Parameter(Mandatory)][string]$BaseUrl
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$runPath = (Get-Item -LiteralPath $RunFolder -ErrorAction Stop).FullName
if (-not (Test-Path -LiteralPath $runPath -PathType Container)) { throw 'RunFolder must be an existing directory.' }
$configFile = [IO.Path]::GetFullPath($ConfigPath)
$executable = Join-Path $PSScriptRoot 'MetaThought.exe'
foreach ($path in @($configFile,$executable)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required input is missing: $path" }
}
$baseUri = $null
if (-not [Uri]::TryCreate($BaseUrl,[UriKind]::Absolute,[ref]$baseUri) -or $baseUri.Scheme -notin @('http','https') -or $baseUri.UserInfo -or $baseUri.Query -or $baseUri.Fragment) {
    throw 'BaseUrl must be an absolute HTTP(S) URL without credentials, a query or a fragment.'
}
if (-not $baseUri.AbsoluteUri.EndsWith('/')) { $baseUri = [Uri]::new($baseUri.AbsoluteUri + '/') }

$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ')
$prefix = Join-Path $runPath "dashboard-publish-$stamp"
$stdoutPath = "$prefix.stdout.txt"
$stderrPath = "$prefix.stderr.txt"
$receiptPath = "$prefix.receipt.json"
foreach ($path in @($stdoutPath,$stderrPath,$receiptPath)) {
    if (Test-Path -LiteralPath $path) { throw "Preserve the existing publication evidence: $path" }
}
$arguments = @('dashboard','-dir',$runPath,'-config',$configFile)
$started = [DateTime]::UtcNow
$nativeExit = -1
$status = 'failed'
$failure = $null
$native = $null
$dashboardUrl = $null
$httpStatus = $null
$localMatchesPublished = $null
$httpMatchesPublished = $null
Push-Location -LiteralPath $PSScriptRoot
try {
    & $executable @arguments 1> $stdoutPath 2> $stderrPath
    $nativeExit = $LASTEXITCODE
    if ($nativeExit -ne 0) { throw "Native dashboard command exited $nativeExit; original stderr is preserved." }
    $native = [IO.File]::ReadAllText($stdoutPath) | ConvertFrom-Json -AsHashtable -Depth 20
    if ($native.state_mode -ne 'ro' -or $native.watch -ne $false -or -not $native.dashboard -or -not $native.published_path) {
        throw 'Native dashboard result must identify read-only state, a one-shot snapshot, the local HTML and its published_path.'
    }
    foreach ($path in @([string]$native.dashboard,[string]$native.published_path)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Native dashboard output is missing: $path" }
    }
    $filename = [IO.Path]::GetFileName([string]$native.published_path)
    $dashboardUrl = [Uri]::new($baseUri,[Uri]::EscapeDataString($filename)).AbsoluteUri
    $generatedText = [IO.File]::ReadAllText([string]$native.dashboard)
    $publishedText = [IO.File]::ReadAllText([string]$native.published_path)
    $localMatchesPublished = [string]::Equals($generatedText,$publishedText,[StringComparison]::Ordinal)
    if (-not $localMatchesPublished) { throw 'Published HTML differs from the generated instance HTML.' }
    $response = Invoke-WebRequest -Uri $dashboardUrl -Method Get -MaximumRedirection 0 -SkipHttpErrorCheck -Headers @{'Cache-Control'='no-cache'}
    $httpStatus = [int]$response.StatusCode
    $httpMatchesPublished = [string]::Equals([string]$response.Content,$publishedText,[StringComparison]::Ordinal)
    if ($httpStatus -ne 200 -or -not $httpMatchesPublished) { throw "HTTP verification failed: status=$httpStatus; exact HTML match=$httpMatchesPublished." }
    $status = 'published_and_verified'
}
catch { $failure = $_.Exception.Message }
finally {
    Pop-Location
    $finished = [DateTime]::UtcNow
    $receipt = [ordered]@{
        operation='publish saved instance dashboard'; status=$status; executable=$executable; config=$configFile;
        arguments=$arguments; run_folder=$runPath; native_exit_code=$nativeExit; native_result=$native;
        url=$dashboardUrl; http_status=$httpStatus; generated_matches_published=$localMatchesPublished;
        http_matches_published=$httpMatchesPublished; provider_calls=$false; server_configuration_changed=$false;
        started_utc=$started.ToString('o'); finished_utc=$finished.ToString('o'); elapsed_seconds=($finished-$started).TotalSeconds;
        error=$failure; stdout=$stdoutPath; stderr=$stderrPath
    }
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes(($receipt | ConvertTo-Json -Depth 8))
    $stream = [IO.File]::Open($receiptPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write)
    try { $stream.Write($bytes,0,$bytes.Length) }
    finally { $stream.Dispose() }
}
[ordered]@{status=$status;url=$dashboardUrl;receipt=$receiptPath;native_exit_code=$nativeExit;http_status=$httpStatus;exact_html_match=$httpMatchesPublished;provider_calls=$false} | ConvertTo-Json -Compress
if ($failure) {
    [Console]::Error.WriteLine($failure)
    if (Test-Path -LiteralPath $stderrPath -PathType Leaf) { [Console]::Error.Write([IO.File]::ReadAllText($stderrPath)) }
    exit 1
}
exit 0
