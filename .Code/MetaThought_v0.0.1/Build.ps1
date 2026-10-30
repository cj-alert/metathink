param([ValidateSet("deps","test","build","all")][string]$Step = "all")
$ErrorActionPreference = "Stop"
$goExe = "C:\Program Files\Go\bin\go.exe"
if (-not (Test-Path -LiteralPath $goExe -PathType Leaf)) { throw "Go executable missing: $goExe" }
$env:GOPROXY = "https://proxy.golang.org"
$env:GOSUMDB = "sum.golang.org"
$env:GOTOOLCHAIN = "local"
$env:CGO_ENABLED = "0"
$env:GOFLAGS = "-buildvcs=false"
Set-Location -LiteralPath $PSScriptRoot
if ($Step -in @("deps", "all")) {
  & $goExe mod download
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if ($Step -in @("test", "all")) {
  & $goExe test ./... -count=1
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if ($Step -in @("build", "all")) {
  & $goExe build -trimpath -o MetaThought.exe .
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
exit 0
