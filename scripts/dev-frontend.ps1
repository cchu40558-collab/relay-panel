param(
    [int]$Port = 5173
)

$ErrorActionPreference = "Stop"

$envScript = Join-Path $PSScriptRoot "dev-env.ps1"
. $envScript

$frontendRoot = Join-Path $ProjectRoot "frontend"
Set-Location $frontendRoot

Write-Host "Frontend URL: http://127.0.0.1:$Port/"
npm.cmd run dev -- --host 127.0.0.1 --port $Port
exit $LASTEXITCODE
