#requires -Version 7.0
[CmdletBinding()]
param([Parameter(Mandatory)][string]$RequestPath)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$m = [IO.File]::ReadAllText((Get-Item -LiteralPath $RequestPath).FullName) | ConvertFrom-Json -AsHashtable
foreach ($key in @('run_folder','prior_handoff','amendment','feedback')) {
    if (-not ($m[$key] -is [string]) -or [string]::IsNullOrWhiteSpace($m[$key])) { throw "Missing $key." }
}
if ($m.confirmed -ne $true) { throw 'An explicit user-confirmed amendment is required.' }
$prior = [IO.File]::ReadAllText($m.prior_handoff)
$amendment = [IO.File]::ReadAllText($m.amendment)
if ([string]::IsNullOrWhiteSpace($prior) -or [string]::IsNullOrWhiteSpace($amendment)) { throw 'Both original handoff and amendment must be nonempty.' }
# The amendment is retained verbatim; only its required local score tail is checked.
$lines = @($amendment.TrimEnd() -split '\r?\n')
if ($lines.Count -lt 3) { throw 'Amendment needs its explanation and two actual local scores.' }
foreach ($line in @($lines[-2],$lines[-1])) {
    $number = 0.0
    if (-not [double]::TryParse($line.Trim(),[Globalization.NumberStyles]::Float,[Globalization.CultureInfo]::InvariantCulture,[ref]$number) -or -not [double]::IsFinite($number) -or $number -lt 0 -or $number -gt 1) { throw 'Amendment local scores must be finite0..1 numbers.' }
}
$run = [IO.Path]::GetFullPath($m.run_folder)
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ')
$composite = Join-Path $run "handoff-amended-$stamp.md"
$text = $prior + [Environment]::NewLine + [Environment]::NewLine + '# User-confirmed amendment — later direction controls the novelty evidence method' + [Environment]::NewLine + $amendment
$stream = [IO.File]::Open($composite,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write)
try {
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes($text)
    $stream.Write($bytes,0,$bytes.Length)
} finally { $stream.Dispose() }
$exe = Join-Path $PSScriptRoot 'MetaThought.exe'
& $exe worker-revise -dir $run -handoff $composite -feedback $m.feedback -confirm
$nativeExit = $LASTEXITCODE
if ($nativeExit -ne 0) { throw "Native contract revision exited $nativeExit; preserve the composite and diagnostics." }
[ordered]@{operation='append user-confirmed amendment and apply native revision';composite=$composite;prior_handoff=$m.prior_handoff;amendment=$m.amendment;feedback=$m.feedback;provider_calls=0;hive_started=$false} | ConvertTo-Json -Compress
