param(
    [int]$Port = 2053
)

$ErrorActionPreference = "Stop"

$envScript = Join-Path $PSScriptRoot "dev-env.ps1"
. $envScript -Port $Port

$go = Join-Path $env:GOROOT "bin\go.exe"
& $go run . run
exit $LASTEXITCODE
