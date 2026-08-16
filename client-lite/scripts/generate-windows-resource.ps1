param(
    [ValidateSet("amd64", "386")]
    [string]$Architecture = "amd64",
    [string]$Output
)

$ErrorActionPreference = "Stop"
$projectDir = Split-Path -Parent $PSScriptRoot
$icon = Join-Path $projectDir "assets\easy-net-lite.ico"
if (-not $Output) {
    $Output = Join-Path $projectDir "cmd\easy-net\easy_net_lite_windows.syso"
}
if (-not (Test-Path -LiteralPath $icon -PathType Leaf)) {
    throw "缺少 Easy-Net Lite 图标资源：$icon"
}

go run github.com/akavel/rsrc@v0.10.2 -arch $Architecture -ico $icon -o $Output
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Windows 图标资源已生成：$Output"
