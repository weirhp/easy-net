@echo off
echo =================================================
echo [OmniGate] Preparing to deploy Cloudflare Worker...
echo =================================================

:: 1. Check Node.js
where node >nul 2>nul
if %errorlevel% neq 0 (
    echo [ERROR] Node.js was not found in your environment path. Please install Node.js first!
    echo Download: https://nodejs.org/
    pause
    exit /b 1
)

:: 2. Deploy Worker using local .env token
echo.
echo Deploying Worker via: npx wrangler deploy
echo.

npx --registry=https://registry.npmmirror.com wrangler deploy

if %errorlevel% neq 0 (
    echo.
    echo [ERROR] Deployment failed!
    echo.
    echo Please make sure:
    echo 1. Your VPN/Proxy is turned ON (to access api.cloudflare.com).
    echo 2. The CLOUDFLARE_API_TOKEN in your .env file is correct.
    echo.
    pause
    exit /b 1
)

echo.
echo =================================================
echo [SUCCESS] Cloudflare Worker deployed successfully!
echo =================================================
pause
