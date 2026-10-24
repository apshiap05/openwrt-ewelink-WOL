param(
    [string] $GoExecutable = 'go'
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$agentDir = Join-Path $projectRoot 'agent'
$deployDir = Join-Path $projectRoot 'deploy'

& $GoExecutable -C $agentDir test ./...
if ($LASTEXITCODE -ne 0) { throw 'Go 测试失败' }
& $GoExecutable -C $agentDir vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Go vet 失败' }

$env:CGO_ENABLED = '0'
$env:GOOS = 'linux'
$env:GOARCH = 'arm64'
$binary = Join-Path $deployDir 'ewelink-wol'
& $GoExecutable -C $agentDir build -trimpath '-ldflags=-s -w' -o $binary .
if ($LASTEXITCODE -ne 0) { throw 'ARM64 编译失败' }
Copy-Item -LiteralPath (Join-Path $projectRoot 'LICENSE') -Destination (Join-Path $deployDir 'LICENSE-project.txt') -Force

$files = @(
    '95-ewelink-wol', 'advanced.lua', 'app.htm', 'app.js', 'config.json',
    'controller.lua', 'ewelink-wol', 'ewelink-wol.init', 'install.sh',
    'LICENSE-project.txt', 'LICENSE-go.txt', 'LICENSE-gorilla-websocket.txt', 'login.lua',
    'manage.lua', 'status.lua', 'test-manage.lua', 'view_model.lua'
)
$manifest = ($files | ForEach-Object {
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $deployDir $_)).Hash.ToLowerInvariant()
    "$hash  $_"
}) -join "`n"
[System.IO.File]::WriteAllText((Join-Path $deployDir 'SHA256SUMS'), "$manifest`n", [System.Text.Encoding]::ASCII)

$archive = Join-Path $projectRoot 'ewelink-wol-openwrt-arm64.tar.gz'
tar -czf $archive -C $deployDir @files 'SHA256SUMS'
if ($LASTEXITCODE -ne 0) { throw '打包失败' }
$packageHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
Write-Output "Package: $archive"
Write-Output "SHA256: $packageHash"
