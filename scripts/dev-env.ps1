param(
    [int]$Port = 2053
)

$ErrorActionPreference = "Stop"

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$ToolRoot = "D:\dm"
$GoRoot = Join-Path $ToolRoot "go"
$GoPath = Join-Path $ToolRoot "gopath"
$NpmCache = Join-Path $ToolRoot "npm-cache"
$GccBin = Join-Path $ToolRoot "c++\msys64\ucrt64\bin"
$DevRoot = Join-Path $ProjectRoot ".dev"
$XuiDevRoot = Join-Path $DevRoot "x-ui"

function Add-PathOnce {
    param([string]$PathItem)

    if ([string]::IsNullOrWhiteSpace($PathItem)) {
        return
    }

    $items = @($env:Path -split ";" | Where-Object { $_ -ne "" })
    if ($items -notcontains $PathItem) {
        $env:Path = $PathItem + ";" + $env:Path
    }
}

New-Item -ItemType Directory -Force -Path $GoPath, $NpmCache, $XuiDevRoot | Out-Null

$env:GOROOT = $GoRoot
$env:GOPATH = $GoPath
$env:GOMODCACHE = Join-Path $GoPath "pkg\mod"
$env:npm_config_cache = $NpmCache

Add-PathOnce (Join-Path $GoRoot "bin")
Add-PathOnce (Join-Path $GoPath "bin")
Add-PathOnce $GccBin

$env:XUI_DEBUG = "true"
$env:XUI_DB_FOLDER = $XuiDevRoot
$env:XUI_LOG_FOLDER = $XuiDevRoot
$env:XUI_BIN_FOLDER = $XuiDevRoot
$env:XUI_INIT_WEB_BASE_PATH = "/"
$env:XUI_PORT = [string]$Port
$env:XUI_SKIP_HSTS = "true"
$env:XUI_ENABLE_FAIL2BAN = "false"

Set-Location $ProjectRoot

Write-Host "Project: $ProjectRoot"
Write-Host "Go: $GoRoot"
Write-Host "Dev data: $XuiDevRoot"
Write-Host "Panel URL: http://127.0.0.1:$Port/panel/"
