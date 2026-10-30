#requires -Version 7.0
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$RunFolder,
    [Parameter(Mandatory)][string]$ResultPath,
    [Parameter(Mandatory)][string]$ReceiptPath,
    [switch]$ValidateOnly
)

$ErrorActionPreference = 'Stop'
$utf8 = [Text.UTF8Encoding]::new($false)
$receipt = $null
$reserved = $false
$helperInvoked = $false

function Write-AlertReceipt {
    $json = $script:receipt | ConvertTo-Json -Depth 6
    [IO.File]::WriteAllText($script:receiptFile, $json, $script:utf8)
}

try {
    $runPath = [IO.Path]::GetFullPath($RunFolder)
    $resultFile = [IO.Path]::GetFullPath($ResultPath)
    $receiptFile = [IO.Path]::GetFullPath($ReceiptPath)
    if (Test-Path -LiteralPath $receiptFile) {
        [ordered]@{status='skipped_existing_receipt';receipt=$receiptFile;sent=$false} | ConvertTo-Json -Compress
        return
    }
    if (-not [string]::Equals([IO.Path]::GetDirectoryName($resultFile), $runPath.TrimEnd([IO.Path]::DirectorySeparatorChar), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Result must belong to the selected run folder.'
    }
    $settings = [IO.File]::ReadAllText((Join-Path $runPath 'config.json')) | ConvertFrom-Json -AsHashtable -Depth 100
    $result = [IO.File]::ReadAllText($resultFile) | ConvertFrom-Json -AsHashtable -Depth 100
    $alert = $settings.alerts.http_500_sms
    if ($null -eq $alert -or $alert.enabled -isnot [bool] -or $alert.enabled -ne $true) {
        [ordered]@{status='disabled';sent=$false} | ConvertTo-Json -Compress
        return
    }
    if ($result.status -cne 'provider_unavailable' -or $result.ready_for_resume -isnot [bool] -or $result.ready_for_resume -ne $true -or $result.provider_status_code -ne 500) {
        [ordered]@{status='not_applicable';sent=$false} | ConvertTo-Json -Compress
        return
    }
    $helper = [string]$alert.helper_path
    if (-not [IO.Path]::IsPathFullyQualified($helper) -or -not (Test-Path -LiteralPath $helper -PathType Leaf)) {
        throw 'Configured SMS helper is missing or is not an absolute path.'
    }
    $project = ([string]$alert.project -replace '[\r\n]+',' ').Trim()
    $titleWords = @((([string]$alert.chat_label).Trim() -split '\s+') | Where-Object { $_ } | Select-Object -First 3)
    if ([string]::IsNullOrWhiteSpace($project) -or $titleWords.Count -ne 3) {
        throw 'SMS project and three-word chat label must be configured.'
    }
    $title = $titleWords -join ' '
    $runName = [IO.Path]::GetFileName($runPath.TrimEnd([IO.Path]::DirectorySeparatorChar))
    $message = "${project}: [$title] -`r- HTTP 500 stopped $runName.`r- Work is saved; ready for explicit resume.`r- Conclusion For Founder: Provider unavailable; we have paused."
    if ($message.Length -lt 110 -or $message.Length -gt 300) {
        throw 'Configured SMS message must be between 110 and 300 characters.'
    }
    if ($ValidateOnly) {
        $validation = @(& $helper -Mode Notification -Message $message -ValidateOnly 2>$null)
        if ($validation.Count -ne 1 -or $validation[0].Valid -ne $true) {
            throw 'SMS configuration validation was not confirmed.'
        }
        [ordered]@{status='validated';valid=$true;mode='Notification';message_length=$message.Length;sent=$false;receipt_created=$false} | ConvertTo-Json -Compress
        return
    }

    # Reserve this attempt before any send. A crash or ambiguous response must
    # leave a receipt that blocks replay, even if acceptance cannot be recorded.
    $receipt = [ordered]@{
        operation='worker HTTP 500 SMS';status='attempt_reserved';created_utc=[DateTime]::UtcNow.ToString('o');
        run_folder=$runPath;result=$resultFile;mode='Notification';message=$message;
        trigger=[ordered]@{status=$result.status;ready_for_resume=$true;provider_status_code=500};
        accepted=$false;helper_invoked=$false;polling=$false;automatic_retry=$false
    }
    try {
        $stream = [IO.File]::Open($receiptFile, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    }
    catch [IO.IOException] {
        if (Test-Path -LiteralPath $receiptFile) {
            [ordered]@{status='skipped_existing_receipt';receipt=$receiptFile;sent=$false} | ConvertTo-Json -Compress
            return
        }
        throw
    }
    try {
        $bytes = $utf8.GetBytes(($receipt | ConvertTo-Json -Depth 6))
        $stream.Write($bytes, 0, $bytes.Length)
        $stream.Flush($true)
        $reserved = $true
    }
    finally { $stream.Dispose() }

    $helperInvoked = $true
    $receipt.helper_invoked = $true
    Write-AlertReceipt
    $response = @(& $helper -Mode Notification -Message $message 2>$null)
    if ($response.Count -ne 1 -or $response[0].Accepted -isnot [bool]) {
        throw 'SMS acceptance was not confirmed.'
    }
    $accepted = $response[0].Accepted -eq $true
    $receipt.accepted = $accepted
    $receipt.status = if ($accepted) { 'accepted' } else { 'not_accepted' }
    # Only the canonical helper's sanitized notification receipt is retained.
    $receipt.helper_receipt = [ordered]@{
        Accepted=$accepted;Status=[string]$response[0].Status;Mode=[string]$response[0].Mode;
        MessageLength=$response[0].MessageLength;PollRequired=$response[0].PollRequired
    }
    $receipt.finished_utc = [DateTime]::UtcNow.ToString('o')
    Write-AlertReceipt
    if (-not $accepted) { [Console]::Error.WriteLine('Worker SMS alert was not accepted; no automatic retry was made.') }
    [ordered]@{status=$receipt.status;accepted=$accepted;receipt=$receiptFile} | ConvertTo-Json -Compress
}
catch {
    $status = if ($helperInvoked) { 'uncertain' } elseif ($ValidateOnly) { 'validation_failed' } else { 'alert_failed' }
    if ($reserved) {
        $receipt.status = $status
        $receipt.finished_utc = [DateTime]::UtcNow.ToString('o')
        $receipt.helper_invoked = $helperInvoked
        try { Write-AlertReceipt } catch { }
    }
    # Never print a helper exception: it could contain private transport details.
    [Console]::Error.WriteLine("Worker SMS alert $status; saved worker state is unchanged. No automatic retry was made.")
    [ordered]@{status=$status;accepted=$false;receipt=$ReceiptPath;receipt_created=$reserved;automatic_retry=$false} | ConvertTo-Json -Compress
}
