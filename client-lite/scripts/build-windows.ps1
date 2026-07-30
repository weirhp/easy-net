$ErrorActionPreference = "Stop"

$projectDir = Split-Path -Parent $PSScriptRoot
$outputDir = Join-Path $projectDir "dist"
$outputFile = Join-Path $outputDir "Easy-Net-Lite.exe"
$versionFile = Join-Path $projectDir "VERSION"
$version = if ($env:EASY_NET_VERSION) { $env:EASY_NET_VERSION.Trim() } else { (Get-Content -Raw $versionFile).Trim() }

if ($version -notmatch '^\d+\.\d+\.\d+([-.][0-9A-Za-z.-]+)?$') {
    throw "无效版本号：$version"
}

New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
$env:CGO_ENABLED = "0"

Push-Location $projectDir
try {
    go test ./...
    go build -trimpath -ldflags "-s -w -H=windowsgui -X easy-net/client-lite/internal/version.Value=$version" -o $outputFile ./cmd/easy-net
} finally {
    Pop-Location
}

Write-Host "构建完成：$outputFile（版本 $version）"
