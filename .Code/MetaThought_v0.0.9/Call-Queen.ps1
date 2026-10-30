#requires -Version 7.0
[CmdletBinding()]
param(
 [Parameter(Mandatory)][string]$PromptPath,
 [Parameter(Mandatory)][string]$RunFolder,
 [string]$RolePath,
 [string]$ConfigPath = (Join-Path $PSScriptRoot 'config.json')
)
$ErrorActionPreference = 'Stop'
$runPath = [IO.Path]::GetFullPath($RunFolder)
if (Test-Path -LiteralPath $runPath) { throw 'Preserve the existing folder; use a fresh output folder.' }
$promptFile = (Get-Item -LiteralPath $PromptPath -ErrorAction Stop).FullName
$configFile = (Get-Item -LiteralPath $ConfigPath -ErrorAction Stop).FullName
$promptText = [IO.File]::ReadAllText($promptFile)
if ([string]::IsNullOrWhiteSpace($promptText)) { throw 'The prompt is empty.' }
$roleText = $null
if ($RolePath) {
 $roleText = [IO.File]::ReadAllText((Get-Item -LiteralPath $RolePath -ErrorAction Stop).FullName)
 if ([string]::IsNullOrWhiteSpace($roleText)) { throw 'The role prompt is empty.' }
}
$settings = [IO.File]::ReadAllText($configFile) | ConvertFrom-Json
$endpoint = [Uri]($settings.meta.base_url.TrimEnd('/') + '/chat/completions')
if ($endpoint.Scheme -ne 'https' -or $endpoint.UserInfo -or $endpoint.Query -or $endpoint.Fragment) { throw 'Meta endpoint must be HTTPS without embedded credentials, query or fragment.' }
$model = [string]$settings.models.queen
if ([string]::IsNullOrWhiteSpace($model)) { throw 'Queen model is required.' }
$effort = [string]$settings.meta.model_reasoning_effort.$model
if (-not $effort) { $effort = [string]$settings.meta.reasoning_effort }
# Connection settings are read locally. Messages contain only the supplied role and exact user prompt.
$apiKey = ''
if ($settings.meta.api_key_env) { $apiKey = [Environment]::GetEnvironmentVariable($settings.meta.api_key_env) }
if ([string]::IsNullOrWhiteSpace($apiKey)) {
 $keyText = [IO.File]::ReadAllText($settings.meta.api_key_file).TrimStart([char]0xFEFF)
 if ($settings.meta.api_key_file_format -eq 'single_value_line') {
  $values = @($keyText -split '\r?\n' | ForEach-Object { $_.Trim() } | Where-Object { $_ -and $_ -notmatch '\s' -and $_ -notmatch '^(#|//|;)' -and $_.TrimEnd(':').ToLowerInvariant() -notin @('meta','api','key','token','credential','credentials','apikey','api_key','api-key','meta_api','meta.api') })
  if ($values.Count -ne 1) { throw 'Meta credential file requires one standalone value.' }
  $apiKey = $values[0]
 } else { $apiKey = $keyText.Trim() }
}
$apiKey = $apiKey.Trim()
if (-not $apiKey -or $apiKey.Contains([char]10) -or $apiKey.Contains([char]13)) { throw 'Meta credential must be one nonempty value.' }
$messages = @()
if ($null -ne $roleText) { $messages += [ordered]@{role='developer';content=$roleText} }
$messages += [ordered]@{role='user';content=$promptText}
$payload = [ordered]@{model=$model;messages=$messages}
if ($effort) { $payload.reasoning_effort = $effort }
$requestText = $payload | ConvertTo-Json -Depth 6 -Compress
[IO.Directory]::CreateDirectory($runPath) | Out-Null
$utf8 = [Text.UTF8Encoding]::new($false)
[IO.File]::WriteAllText((Join-Path $runPath 'prompt.txt'),$promptText,$utf8)
if ($null -ne $roleText) { [IO.File]::WriteAllText((Join-Path $runPath 'role.txt'),$roleText,$utf8) }
[IO.File]::WriteAllText((Join-Path $runPath 'request.json'),$requestText,$utf8)
$eventPath = Join-Path $runPath 'events.jsonl'
function Write-Event([string]$Name, $Data) {
 $event = [ordered]@{ts=[DateTime]::UtcNow.ToString('o');event=$Name;data=$Data}
 [IO.File]::AppendAllText($eventPath,($event | ConvertTo-Json -Depth 12 -Compress)+[Environment]::NewLine,$utf8)
}
$handler = [Net.Http.HttpClientHandler]::new()
$handler.AllowAutoRedirect = $false
$client = [Net.Http.HttpClient]::new($handler)
$client.Timeout = [Threading.Timeout]::InfiniteTimeSpan
$request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Post,$endpoint)
$request.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer',$apiKey)
$request.Content = [Net.Http.StringContent]::new($requestText,$utf8,'application/json')
$started = [DateTime]::UtcNow
$statusCode = $null
$usage = $null
$actualModel = $null
$finishReason = $null
$errorText = $null
$outcome = 'error'
$response = $null
Write-Event 'llm.start' ([ordered]@{model=$model;endpoint=$endpoint.AbsoluteUri;request_file='request.json'})
try {
 # Deliberately one send: no retries, tools, conversation ID, or other LLM calls.
 $response = $client.SendAsync($request).GetAwaiter().GetResult()
 $statusCode = [int]$response.StatusCode
 $body = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
 $safeBody = $body.Replace($apiKey,'[REDACTED]')
 [IO.File]::WriteAllText((Join-Path $runPath 'response.json'),$safeBody,$utf8)
 Write-Event 'llm.response' ([ordered]@{http_status=$statusCode;response_file='response.json'})
 if (-not $response.IsSuccessStatusCode) { throw ('HTTP '+$statusCode+': '+$safeBody) }
 $wire = $body | ConvertFrom-Json
 if (@($wire.choices).Count -ne 1) { throw 'Meta response does not contain one completion.' }
 $choice = $wire.choices[0]
 $actualModel = $wire.model
 $usage = $wire.usage
 $finishReason = $choice.finish_reason
 $reply = $choice.message.content
 if ($null -ne $reply -and $reply -isnot [string]) { throw 'Meta completion content is not text.' }
 if ($null -ne $reply) { [IO.File]::WriteAllText((Join-Path $runPath 'reply.md'),$reply,$utf8) }
 if ($choice.message.refusal) {
  [IO.File]::WriteAllText((Join-Path $runPath 'refusal.txt'),[string]$choice.message.refusal,$utf8)
  throw 'Meta returned a refusal; original response preserved.'
 }
 if ($finishReason -ne 'stop') { throw ('Meta completion ended with '+$finishReason+'; original response preserved.') }
 if ([string]::IsNullOrWhiteSpace($reply)) { throw 'Meta returned no text.' }
 $fence = [string]::new([char]96, 4)
 $exchange = @('# Complete Queen exchange', '')
 foreach ($message in $messages) {
  $exchange += @(('## ' + $message.role + ' message'), '', ($fence + 'plaintext'), [string]$message.content, $fence, '')
 }
 $exchange += @('## Queen response (verbatim)', '', ($fence + 'plaintext'), [string]$reply, $fence, '')
 [IO.File]::WriteAllText((Join-Path $runPath 'complete-exchange.md'),[string]::Join([Environment]::NewLine,[string[]]$exchange),$utf8)
 $outcome = 'answered'
}
catch { $errorText = $_.Exception.Message.Replace($apiKey,'[REDACTED]') }
finally {
 $finished = [DateTime]::UtcNow
 $receipt = [ordered]@{model=$model;actual_model=$actualModel;reasoning_effort=$effort;http_status=$statusCode;finish_reason=$finishReason;outcome=$outcome;error=$errorText;usage=$usage;started_utc=$started.ToString('o');finished_utc=$finished.ToString('o');elapsed_seconds=($finished-$started).TotalSeconds;endpoint=$endpoint.AbsoluteUri;request='request.json';response='response.json';reply='reply.md';messages=$messages.Count;hive_started=$false}
 [IO.File]::WriteAllText((Join-Path $runPath 'receipt.json'),($receipt | ConvertTo-Json -Depth 10),$utf8)
 Write-Event 'llm.finish' $receipt
 if ($response) { $response.Dispose() }
 $request.Dispose()
 $client.Dispose()
}
[ordered]@{outcome=$outcome;http_status=$statusCode;error=$errorText;receipt=(Join-Path $runPath 'receipt.json');reply=(Join-Path $runPath 'reply.md')} | ConvertTo-Json -Compress
if ($errorText) { [Console]::Error.WriteLine($errorText); exit 1 }
exit 0
