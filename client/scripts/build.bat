@echo off
echo =================================================
echo [Easy-Net Go] Preparing to compile Go client...
echo =================================================

:: 1. Ensure we are in the project root
cd /d "%~dp0\.."

:: 2. Set Go proxy for China mainland users to download dependencies fast
set GOPROXY=https://goproxy.cn,direct
echo [GOPROXY] Set GOPROXY to https://goproxy.cn

:: 3. Tidy dependencies
echo [1/3] Running 'go mod tidy'...
go mod tidy
if %errorlevel% neq 0 (
    echo [ERROR] Failed to tidy dependencies.
    pause
    exit /b 1
)

if not exist dist mkdir dist

:: 4. Build console version
echo [2/3] Building console version (easy-net-manager.exe)...
go build -ldflags="-s -w" -o dist/easy-net-manager.exe ./src
if %errorlevel% neq 0 (
    echo [ERROR] Failed to build console version.
    pause
    exit /b 1
)

:: 5. Build silent background version
echo [3/3] Building silent background version (easy-net-manager-silent.exe)...
go build -ldflags="-s -w -H=windowsgui" -o dist/easy-net-manager-silent.exe ./src
if %errorlevel% neq 0 (
    echo [ERROR] Failed to build silent background version.
    pause
    exit /b 1
)

echo =================================================
echo [SUCCESS] Compilation completed! Generated files:
echo 1. dist/easy-net-manager.exe         - Console version (shows logs)
echo 2. dist/easy-net-manager-silent.exe  - Silent background version (runs hidden)
echo =================================================
pause
