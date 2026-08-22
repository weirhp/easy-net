param(
    [string]$Destination = (Join-Path (Split-Path -Parent $PSScriptRoot) "build-x64\Release\zeroomega")
)

$ErrorActionPreference = "Stop"
$repository = "zero-peak/ZeroOmega"
$releaseApi = "https://api.github.com/repos/$repository/releases/latest"
$archiveName = "chromium-release.zip"
$checksumName = "$archiveName.sha256"
$headers = @{
    "Accept" = "application/vnd.github+json"
    "User-Agent" = "Easy-Net-Hook-Packager"
    "X-GitHub-Api-Version" = "2022-11-28"
}
if ($env:GITHUB_TOKEN) {
    $headers["Authorization"] = "Bearer $($env:GITHUB_TOKEN)"
}
$downloadHeaders = $headers.Clone()
$downloadHeaders["Accept"] = "application/octet-stream"
$temporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("easy-net-zeroomega-" + [guid]::NewGuid())
$archivePath = Join-Path $temporaryDirectory $archiveName
$checksumPath = Join-Path $temporaryDirectory $checksumName

function Invoke-JsonRequestWithRetry([string]$Uri) {
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        try {
            return Invoke-RestMethod -Headers $headers -TimeoutSec 60 -Uri $Uri
        } catch {
            if ($attempt -eq 3) { throw }
            Write-Warning "Request failed (attempt $attempt/3): $($_.Exception.Message)"
            Start-Sleep -Seconds (2 * $attempt)
        }
    }
}

function Invoke-DownloadWithRetry([string]$Uri, [string]$OutFile, [int]$TimeoutSec = 300) {
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        try {
            Invoke-WebRequest -UseBasicParsing -Headers $downloadHeaders `
                -TimeoutSec $TimeoutSec -Uri $Uri -OutFile $OutFile
            return
        } catch {
            if ($attempt -eq 3) { throw }
            Write-Warning "Download failed (attempt $attempt/3): $($_.Exception.Message)"
            Start-Sleep -Seconds (2 * $attempt)
        }
    }
}

try {
    New-Item -ItemType Directory -Force $temporaryDirectory | Out-Null
    New-Item -ItemType Directory -Force $Destination | Out-Null

    Write-Host "Resolving the latest ZeroOmega release..."
    $release = Invoke-JsonRequestWithRetry $releaseApi
    if (-not $release.tag_name -or $release.draft -or $release.prerelease) {
        throw "The ZeroOmega latest-release response is not a stable published release."
    }

    $archiveAsset = @($release.assets) | Where-Object { $_.name -eq $archiveName }
    $checksumAsset = @($release.assets) | Where-Object { $_.name -eq $checksumName }
    if ($archiveAsset.Count -ne 1 -or $checksumAsset.Count -ne 1) {
        throw "ZeroOmega release $($release.tag_name) does not contain exactly one $archiveName and $checksumName."
    }

    Write-Host "Downloading ZeroOmega $($release.tag_name)..."
    Invoke-DownloadWithRetry $archiveAsset[0].url $archivePath
    Invoke-DownloadWithRetry $checksumAsset[0].url $checksumPath 120

    $checksumText = Get-Content -LiteralPath $checksumPath -Raw
    $checksumMatch = [regex]::Match($checksumText, '(?i)\b[0-9a-f]{64}\b')
    if (-not $checksumMatch.Success) {
        throw "ZeroOmega checksum file does not contain a SHA-256 digest."
    }
    $expectedSha256 = $checksumMatch.Value.ToLowerInvariant()
    $apiDigest = "$($archiveAsset[0].digest)"
    if ($apiDigest -and $apiDigest -ne "sha256:$expectedSha256") {
        throw "ZeroOmega API digest does not match the published checksum file."
    }
    $actualSha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    if ($actualSha256 -ne $expectedSha256) {
        throw "ZeroOmega archive checksum mismatch: $actualSha256"
    }

    Copy-Item -LiteralPath $archivePath -Destination (Join-Path $Destination $archiveName) -Force
    Copy-Item -LiteralPath $checksumPath -Destination (Join-Path $Destination $checksumName) -Force
    $license = Invoke-JsonRequestWithRetry `
        "https://api.github.com/repos/$repository/contents/COPYING?ref=$($release.tag_name)"
    if ($license.encoding -ne "base64" -or -not $license.content) {
        throw "ZeroOmega COPYING file could not be read from the GitHub API."
    }
    $licenseBytes = [Convert]::FromBase64String(($license.content -replace '\s', ''))
    [IO.File]::WriteAllBytes((Join-Path $Destination "COPYING.txt"), $licenseBytes)
    Set-Content -LiteralPath (Join-Path $Destination "VERSION.txt") `
        -Value "ZeroOmega $($release.tag_name)`nAsset: $archiveName`nSource: $($release.html_url)" -Encoding utf8
    Write-Host "ZeroOmega $($release.tag_name) installed in: $Destination"
} finally {
    if (Test-Path -LiteralPath $temporaryDirectory) {
        Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force -ErrorAction SilentlyContinue
    }
}
