@echo off
echo =================================================
echo [Easy-Net Go] Preparing cross-platform compilation...
echo =================================================

cd /d "%~dp0"
set GOPROXY=https://goproxy.cn,direct

echo [1/5] Running 'go mod tidy'...
go mod tidy
if %errorlevel% neq 0 (
    echo [ERROR] Failed to tidy dependencies.
    pause
    exit /b 1
)

:: 1. Windows x64 Version
echo [2/5] Compiling Windows x64 versions...
go build -ldflags="-s -w" -o bin/proxy-windows-amd64.exe main.go
go build -ldflags="-s -w -H=windowsgui" -o bin/proxy-windows-amd64-silent.exe main.go

:: 2. macOS Apple Silicon (M1/M2/M3/M4) Version
echo [3/5] Compiling macOS Apple Silicon (ARM64) version...
set GOOS=darwin
set GOARCH=arm64
go build -ldflags="-s -w" -o bin/proxy-mac-arm64 main.go

:: 3. macOS Intel Version
echo [4/5] Compiling macOS Intel (AMD64) version...
set GOOS=darwin
set GOARCH=amd64
go build -ldflags="-s -w" -o bin/proxy-mac-amd64 main.go

:: 4. Linux x64 Version (Useful for servers or routers)
echo [5/5] Compiling Linux x64 version...
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o bin/proxy-linux-amd64 main.go

:: Reset environment variables for Windows CMD
set GOOS=
set GOARCH=

echo =================================================
echo [SUCCESS] Cross-compilation completed!
echo Generated files are located in the "bin" folder:
echo 1. bin/proxy-windows-amd64.exe       (Windows Console)
echo 2. bin/proxy-windows-amd64-silent.exe (Windows Silent Background)
echo 3. bin/proxy-mac-arm64                (macOS Apple Silicon M1/M2/M3/M4)
echo 4. bin/proxy-mac-amd64                (macOS Intel)
echo 5. bin/proxy-linux-amd64              (Linux Server/Router)
echo =================================================
pause
