@echo off
echo =================================================
echo [Easy-Net Go] Preparing cross-platform compilation...
echo =================================================

cd /d "%~dp0\.."
set GOPROXY=https://goproxy.cn,direct

echo [1/5] Running 'go mod tidy'...
go mod tidy
if %errorlevel% neq 0 (
    echo [ERROR] Failed to tidy dependencies.
    pause
    exit /b 1
)

if not exist dist mkdir dist

:: 1. Windows x64 Version
echo [2/5] Compiling Windows x64 versions...
go build -ldflags="-s -w" -o dist/easy-net-manager-windows-amd64.exe ./src
go build -ldflags="-s -w -H=windowsgui" -o dist/easy-net-manager-windows-amd64-silent.exe ./src

:: 2. macOS Apple Silicon (M1/M2/M3/M4) Version
echo [3/5] Compiling macOS Apple Silicon (ARM64) version...
set GOOS=darwin
set GOARCH=arm64
go build -ldflags="-s -w" -o dist/easy-net-manager-mac-arm64 ./src

:: 3. macOS Intel Version
echo [4/5] Compiling macOS Intel (AMD64) version...
set GOOS=darwin
set GOARCH=amd64
go build -ldflags="-s -w" -o dist/easy-net-manager-mac-amd64 ./src

:: 4. Linux x64 Version (Useful for servers or routers)
echo [5/5] Compiling Linux x64 version...
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o dist/easy-net-manager-linux-amd64 ./src

:: Reset environment variables for Windows CMD
set GOOS=
set GOARCH=

echo =================================================
echo [SUCCESS] Cross-compilation completed!
echo Generated files are located in the "dist" folder:
echo 1. dist/easy-net-manager-windows-amd64.exe        (Windows Console)
echo 2. dist/easy-net-manager-windows-amd64-silent.exe (Windows Silent Background)
echo 3. dist/easy-net-manager-mac-arm64                (macOS Apple Silicon M1/M2/M3/M4)
echo 4. dist/easy-net-manager-mac-amd64                (macOS Intel)
echo 5. dist/easy-net-manager-linux-amd64              (Linux Server/Router)
echo =================================================
pause
