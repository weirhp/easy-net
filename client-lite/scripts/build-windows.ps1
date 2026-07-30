$ErrorActionPreference = "Stop"

$projectDir = Split-Path -Parent $PSScriptRoot
$outputDir = Join-Path $projectDir "dist"
$outputFile = Join-Path $outputDir "Easy-Net-Lite.exe"

New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
$env:CGO_ENABLED = "0"

Push-Location $projectDir
try {
    go test ./...
    go build -trimpath -ldflags "-s -w -H=windowsgui" -o $outputFile ./cmd/easy-net
} finally {
    Pop-Location
}

Write-Host "构建完成：$outputFile"
