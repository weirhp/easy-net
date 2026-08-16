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
    throw "Easy-Net Lite icon resource is missing: $icon"
}

go run github.com/akavel/rsrc@v0.10.2 -arch $Architecture -ico $icon -o $Output
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Windows icon resource generated: $Output"
