param(
    [ValidateSet("x64", "Win32")]
    [string]$Architecture = "x64",
    [ValidateSet("Debug", "Release")]
    [string]$Configuration = "Release",
    [switch]$WithTunEngine
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
$buildDirectory = Join-Path $projectRoot "build-$($Architecture.ToLowerInvariant())"

function Find-VisualStudioInstallation {
    $vswhereCandidates = @(
        "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe",
        "$env:ProgramFiles\Microsoft Visual Studio\Installer\vswhere.exe"
    )
    foreach ($vswhere in $vswhereCandidates) {
        if (-not (Test-Path -LiteralPath $vswhere)) { continue }
        $installation = & $vswhere -latest -products * `
            -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 `
            -property installationPath
        if ($LASTEXITCODE -eq 0 -and $installation) {
            return ($installation | Select-Object -First 1)
        }
    }
    return $null
}

function Find-CMake([string]$visualStudioInstallation) {
    $command = Get-Command cmake -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    if ($visualStudioInstallation) {
        $bundled = Join-Path $visualStudioInstallation `
            "Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe"
        if (Test-Path -LiteralPath $bundled) { return $bundled }
    }
    return $null
}

$visualStudioInstallation = Find-VisualStudioInstallation
$cmake = Find-CMake $visualStudioInstallation
if (-not $visualStudioInstallation -or -not $cmake) {
    throw @"
The MSVC C++ build environment is incomplete.

Install Visual Studio 2022 Build Tools with Desktop development with C++:
  winget install --id Microsoft.VisualStudio.2022.BuildTools -e --override "--wait --passive --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended" --accept-source-agreements --accept-package-agreements

Alternatively, open Visual Studio Installer and select:
  Desktop development with C++
  MSVC x64/x86 build tools
  Windows 10/11 SDK
  C++ CMake tools for Windows
"@
}

$ctest = Join-Path (Split-Path -Parent $cmake) "ctest.exe"
if (-not (Test-Path -LiteralPath $ctest)) {
    $ctestCommand = Get-Command ctest -ErrorAction SilentlyContinue
    if (-not $ctestCommand) { throw "ctest.exe was not found beside CMake or on PATH." }
    $ctest = $ctestCommand.Source
}

Write-Host "Visual Studio: $visualStudioInstallation"
Write-Host "CMake: $cmake"

& $cmake -S $projectRoot -B $buildDirectory -A $Architecture
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& $cmake --build $buildDirectory --config $Configuration --parallel
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& $ctest --test-dir $buildDirectory -C $Configuration --output-on-failure
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$outputDirectory = Join-Path $buildDirectory $Configuration
if ($WithTunEngine) {
    if ($Architecture -ne "x64") {
        throw "The optional TUN engine is available only for x64 builds."
    }
    & (Join-Path $PSScriptRoot "install-tun-engine.ps1") `
        -Destination (Join-Path $outputDirectory "tun")
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
Write-Host "Build complete: $outputDirectory"
