#requires -Version 7.0
[CmdletBinding()]
param([Parameter(Mandatory)][string]$EvidenceFolder)

$ErrorActionPreference = 'Stop'
$evidencePath = [IO.Path]::GetFullPath($EvidenceFolder)
if (Test-Path -LiteralPath $evidencePath) { throw 'Offline alert tests require a new EvidenceFolder.' }
$subject = Join-Path $PSScriptRoot 'Send-WorkerAlert.ps1'
if (-not (Test-Path -LiteralPath $subject -PathType Leaf)) { throw 'Send-WorkerAlert.ps1 is missing.' }
[IO.Directory]::CreateDirectory($evidencePath) | Out-Null
$utf8 = [Text.UTF8Encoding]::new($false)
$checks = [Collections.Generic.List[object]]::new()
$casePaths = [Collections.Generic.List[string]]::new()
$started = [DateTime]::UtcNow
$failure = $null

# This is the only helper available to each fixture. It records local calls and
# returns scripted outcomes; it has no credentials, imports, HTTP or SMS calls.
$fakeHelper = @'
[CmdletBinding()]
param([string]$Mode, [string]$Message, [switch]$ValidateOnly)
$ErrorActionPreference = 'Stop'
$entry = [ordered]@{mode=$Mode;validate_only=[bool]$ValidateOnly;message_length=$Message.Length}
[IO.File]::AppendAllText((Join-Path $PSScriptRoot 'fake-invocations.jsonl'), (($entry | ConvertTo-Json -Compress) + [Environment]::NewLine))
if ($Mode -ne 'Notification') { throw 'Fixture expected Notification mode.' }
if ($Message.Length -lt 110 -or $Message.Length -gt 300) { throw 'Fixture message length is invalid.' }
if ($ValidateOnly) { [pscustomobject]@{Valid=$true;Mode=$Mode;MessageLength=$Message.Length}; return }
$reserved = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'sms.receipt.json')) | ConvertFrom-Json -AsHashtable
if ($reserved.status -ne 'attempt_reserved' -or $reserved.helper_invoked -ne $true) { throw 'Send must have a reserved receipt before invoking helper.' }
$behavior = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'fake-behavior.txt')).Trim()
if ($behavior -eq 'throw') { throw 'Simulated ambiguous helper failure.' }
[pscustomobject]@{Accepted=($behavior -ne 'reject');Status=if ($behavior -eq 'reject') {'failed'} else {'queued'};Mode=$Mode;MessageLength=$Message.Length;PollRequired=$false}
'@

function Write-FixtureJson([string]$Path, $Value) {
    [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 8), $script:utf8)
}

function New-Case([string]$Name, [string]$Behavior = 'accept') {
    $dir = Join-Path $script:evidencePath $Name
    [IO.Directory]::CreateDirectory($dir) | Out-Null
    $script:casePaths.Add($dir)
    $helper = Join-Path $dir 'Fake-Sms.ps1'
    [IO.File]::WriteAllText($helper, $script:fakeHelper, $script:utf8)
    [IO.File]::WriteAllText((Join-Path $dir 'fake-behavior.txt'), $Behavior, $script:utf8)
    $config = @{alerts=@{http_500_sms=@{enabled=$true;helper_path=$helper;project='MetaThought';chat_label='Offline Worker Alert'}}}
    $result = @{status='provider_unavailable';ready_for_resume=$true;provider_status_code=500;provider_attempts=2;error='Fixture HTTP 500'}
    Write-FixtureJson (Join-Path $dir 'config.json') $config
    Write-FixtureJson (Join-Path $dir 'result.json') $result
    return @{Path=$dir;Config=$config;Result=$result;Receipt=(Join-Path $dir 'sms.receipt.json')}
}

function Invoke-Case($Case, [switch]$ValidateOnly) {
    $lines = @(& $script:subject -RunFolder $Case.Path -ResultPath (Join-Path $Case.Path 'result.json') -ReceiptPath $Case.Receipt -ValidateOnly:$ValidateOnly)
    if ($lines.Count -ne 1) { throw 'Alert must return exactly one compact status object.' }
    return ($lines[0] | ConvertFrom-Json -AsHashtable)
}

function Get-Calls($Case) {
    $path = Join-Path $Case.Path 'fake-invocations.jsonl'
    if (Test-Path -LiteralPath $path) {
        foreach ($line in [IO.File]::ReadAllLines($path)) {
            if ($line) { $line | ConvertFrom-Json -AsHashtable }
        }
    }
}

function Check([bool]$Condition, [string]$Name) {
    if (-not $Condition) { throw $Name }
    $script:checks.Add([ordered]@{check=$Name;passed=$true})
}

try {
    $case = New-Case 'eligible'
    $resultBefore = [IO.File]::ReadAllText((Join-Path $case.Path 'result.json'))
    $first = Invoke-Case $case
    $saved = [IO.File]::ReadAllText($case.Receipt) | ConvertFrom-Json -AsHashtable
    Check ($first.status -eq 'accepted' -and $saved.accepted -eq $true -and $saved.helper_receipt.Accepted -eq $true) 'eligible stop records accepted notification'
    Check (@(Get-Calls $case).Count -eq 1) 'eligible stop invokes fake helper once'
    $receiptBefore = [IO.File]::ReadAllText($case.Receipt)
    $replay = Invoke-Case $case
    Check ($replay.status -eq 'skipped_existing_receipt' -and @(Get-Calls $case).Count -eq 1) 'accepted replay cannot resend'
    Check ([IO.File]::ReadAllText($case.Receipt) -ceq $receiptBefore) 'accepted replay preserves receipt exactly'
    Check ([IO.File]::ReadAllText((Join-Path $case.Path 'result.json')) -ceq $resultBefore) 'notification preserves worker result exactly'

    foreach ($name in @('ordinary-error','not-ready','ready-string','not-500','disabled')) {
        $case = New-Case $name
        switch ($name) {
            'ordinary-error' { $case.Result.status = 'failed' }
            'not-ready' { $case.Result.ready_for_resume = $false }
            'ready-string' { $case.Result.ready_for_resume = 'true' }
            'not-500' { $case.Result.provider_status_code = 503 }
            'disabled' { $case.Config.alerts.http_500_sms.enabled = $false }
        }
        Write-FixtureJson (Join-Path $case.Path 'config.json') $case.Config
        Write-FixtureJson (Join-Path $case.Path 'result.json') $case.Result
        $response = Invoke-Case $case
        $expected = if ($name -eq 'disabled') { 'disabled' } else { 'not_applicable' }
        Check ($response.status -eq $expected -and @(Get-Calls $case).Count -eq 0 -and -not (Test-Path -LiteralPath $case.Receipt)) "$name makes no helper call or send receipt"
    }

    $case = New-Case 'validate-only'
    $response = Invoke-Case $case -ValidateOnly
    $calls = @(Get-Calls $case)
    Check ($response.status -eq 'validated' -and $calls.Count -eq 1 -and $calls[0].validate_only -eq $true -and -not (Test-Path -LiteralPath $case.Receipt)) 'validation invokes only fake validation and creates no send receipt'

    foreach ($behavior in @('throw','reject')) {
        $case = New-Case "helper-$behavior" $behavior
        $resultBefore = [IO.File]::ReadAllText((Join-Path $case.Path 'result.json'))
        $response = Invoke-Case $case
        $saved = [IO.File]::ReadAllText($case.Receipt) | ConvertFrom-Json -AsHashtable
        $expected = if ($behavior -eq 'throw') { 'uncertain' } else { 'not_accepted' }
        Check ($response.status -eq $expected -and $saved.status -eq $expected -and $saved.accepted -eq $false -and $saved.automatic_retry -eq $false) "$behavior preserves non-accepted outcome"
        $receiptBefore = [IO.File]::ReadAllText($case.Receipt)
        $replay = Invoke-Case $case
        Check ($replay.status -eq 'skipped_existing_receipt' -and @(Get-Calls $case).Count -eq 1 -and [IO.File]::ReadAllText($case.Receipt) -ceq $receiptBefore) "$behavior replay cannot resend or rewrite receipt"
        Check ([IO.File]::ReadAllText((Join-Path $case.Path 'result.json')) -ceq $resultBefore) "$behavior preserves worker result exactly"
    }
}
catch { $failure = $_.Exception.Message }

$allCalls = @(
    foreach ($dir in $casePaths) { Get-Calls @{Path=$dir} }
)
$exitCode = if ($failure) { 1 } else { 0 }
$receiptPath = Join-Path $evidencePath 'test-receipt.json'
$receipt = [ordered]@{
    operation='offline worker SMS alert tests';started_utc=$started.ToString('o');finished_utc=[DateTime]::UtcNow.ToString('o');
    exit_code=$exitCode;passed=($exitCode -eq 0);failure=$failure;case_count=$casePaths.Count;check_count=$checks.Count;
    fake_helper_calls=$allCalls.Count;fake_send_calls=@($allCalls | Where-Object { -not $_.validate_only }).Count;
    fake_validation_calls=@($allCalls | Where-Object { $_.validate_only }).Count;real_sms_calls=0;network_calls=0;
    checks=$checks.ToArray()
}
Write-FixtureJson $receiptPath $receipt
[ordered]@{passed=$receipt.passed;exit_code=$exitCode;cases=$receipt.case_count;checks=$receipt.check_count;fake_helper_calls=$receipt.fake_helper_calls;fake_send_calls=$receipt.fake_send_calls;fake_validation_calls=$receipt.fake_validation_calls;real_sms_calls=0;receipt=$receiptPath;failure=$failure} | ConvertTo-Json -Compress
exit $exitCode
