#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DIST_DIR="$PROJECT_DIR/dist"
APP_DIR="$DIST_DIR/Easy-Net Lite.app"
CONTENTS_DIR="$APP_DIR/Contents"

if ! xcrun --find clang >/dev/null 2>&1; then
  echo "缺少 Xcode Command Line Tools，请先运行：xcode-select --install" >&2
  exit 1
fi

mkdir -p "$CONTENTS_DIR/MacOS" "$CONTENTS_DIR/Resources"
cp "$PROJECT_DIR/build/macos/Info.plist" "$CONTENTS_DIR/Info.plist"

cd "$PROJECT_DIR"
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o "$CONTENTS_DIR/MacOS/easy-net-lite" ./cmd/easy-net

echo "构建完成：$APP_DIR"
echo "正式分发前请使用 Apple Developer ID 对 .app 签名并公证。"
