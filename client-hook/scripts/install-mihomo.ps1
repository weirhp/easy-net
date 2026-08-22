param(
    [string]$Destination = (Join-Path (Split-Path -Parent $PSScriptRoot) "build-x64\Release\mihomo")
)

$ErrorActionPreference = "Stop"
$version = "1.19.27"
$archiveName = "mihomo-windows-amd64-compatible-v$version.zip"
$downloadUrl = "https://github.com/MetaCubeX/mihomo/releases/download/v$version/$archiveName"
# GitHub Release API asset digest for the exact archive above.
$expectedSha256 = "9cddc00240ed90d0bbd8333c1ff2b83152eb03c6bcddfbb5a17714a3272c1e88"
$temporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("easy-net-mihomo-" + [guid]::NewGuid())
$archivePath = Join-Path $temporaryDirectory $archiveName

try {
    New-Item -ItemType Directory -Force $temporaryDirectory | Out-Null
    New-Item -ItemType Directory -Force $Destination | Out-Null
    Write-Host "Downloading Mihomo v$version..."
    Invoke-WebRequest -UseBasicParsing -TimeoutSec 300 -Uri $downloadUrl -OutFile $archivePath
    $actualSha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    if ($actualSha256 -ne $expectedSha256) {
        throw "Mihomo archive checksum mismatch: $actualSha256"
    }
    Expand-Archive -LiteralPath $archivePath -DestinationPath $temporaryDirectory -Force
    $executable = Get-ChildItem -LiteralPath $temporaryDirectory -Filter "mihomo*.exe" -File -Recurse |
        Select-Object -First 1
    if (-not $executable) {
        throw "mihomo.exe was not found in the downloaded archive."
    }
    Copy-Item -LiteralPath $executable.FullName -Destination (Join-Path $Destination "mihomo.exe") -Force
    Invoke-WebRequest -UseBasicParsing -TimeoutSec 60 `
        -Uri "https://raw.githubusercontent.com/MetaCubeX/mihomo/v$version/LICENSE" `
        -OutFile (Join-Path $Destination "LICENSE.txt")
    Set-Content -LiteralPath (Join-Path $Destination "VERSION.txt") `
        -Value "Mihomo v$version`nSource: https://github.com/MetaCubeX/mihomo" -Encoding utf8
    Write-Host "Mihomo installed in: $Destination"
} finally {
    if (Test-Path -LiteralPath $temporaryDirectory) {
        Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force
    }
}
