param(
    [int]$Port = 2053
)

$ErrorActionPreference = "Stop"

$envScript = Join-Path $PSScriptRoot "dev-env.ps1"
. $envScript -Port $Port

$go = Join-Path $env:GOROOT "bin\go.exe"
$gcc = Join-Path $GccBin "gcc.exe"

Write-Host ""
Write-Host "== Toolchain =="
& $go version
if (Test-Path $gcc) {
    & $gcc --version | Select-Object -First 1
}

Write-Host ""
Write-Host "== Frontend typecheck =="
Set-Location (Join-Path $ProjectRoot "frontend")
npm.cmd run typecheck
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "== Backend tests =="
Set-Location $ProjectRoot
& $go test ./internal/database/model ./internal/web/controller ./internal/web/service
exit $LASTEXITCODE
