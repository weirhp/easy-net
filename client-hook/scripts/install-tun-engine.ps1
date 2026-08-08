param(
    [Parameter(Mandatory = $true)]
    [string]$Destination
)

$ErrorActionPreference = "Stop"
$version = "1.13.16"
$expectedSha256 = "6CBF90EC4EE87122FFCE09B73928FB31E763BC1C75A119F79C61D24734C78807"
$archiveName = "sing-box-$version-windows-amd64.zip"
$downloadUrl = "https://github.com/SagerNet/sing-box/releases/download/v$version/$archiveName"
$temporaryDirectory = Join-Path ([System.IO.Path]::GetTempPath()) `
    "easy-net-tun-$([Guid]::NewGuid().ToString('N'))"

try {
    New-Item -ItemType Directory -Path $temporaryDirectory | Out-Null
    $archive = Join-Path $temporaryDirectory $archiveName
    Write-Host "Downloading optional sing-box TUN engine v$version..."
    Invoke-WebRequest -UseBasicParsing -Uri $downloadUrl -OutFile $archive
    $actualSha256 = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
    if ($actualSha256 -ne $expectedSha256) {
        throw "sing-box archive checksum mismatch: $actualSha256"
    }

    Expand-Archive -LiteralPath $archive -DestinationPath $temporaryDirectory
    $source = Join-Path $temporaryDirectory "sing-box-$version-windows-amd64"
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    Copy-Item -LiteralPath (Join-Path $source "sing-box.exe") -Destination $Destination
    Copy-Item -LiteralPath (Join-Path $source "libcronet.dll") -Destination $Destination
    Copy-Item -LiteralPath (Join-Path $source "LICENSE") `
        -Destination (Join-Path $Destination "sing-box-LICENSE.txt")
    Set-Content -LiteralPath (Join-Path $Destination "VERSION.txt") `
        -Value "sing-box $version`nSource: https://github.com/SagerNet/sing-box" -Encoding utf8
    Write-Host "TUN engine installed in: $Destination"
}
finally {
    if (Test-Path -LiteralPath $temporaryDirectory) {
        Remove-Item -LiteralPath $temporaryDirectory -Recurse -Force
    }
}
