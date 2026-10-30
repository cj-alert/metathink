param([ValidateSet("deps","test","build","all")][string]$Step = "build")
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
  $goFmtExe = "C:\Program Files\Go\bin\gofmt.exe"
  if (-not (Test-Path -LiteralPath $goFmtExe -PathType Leaf)) { throw "Go formatter missing: $goFmtExe" }
  $goSources = @(Get-ChildItem -LiteralPath $PSScriptRoot -File -Filter "*.go" | Where-Object { $_.Name -notlike "*_test.go" } | Select-Object -ExpandProperty FullName)
  if ($goSources.Count -eq 0) { throw "No production Go files found in $PSScriptRoot" }
  & $goFmtExe -w @goSources
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  & $goExe version
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  & $goExe build -trimpath -o MetaThought.exe .
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
exit 0
