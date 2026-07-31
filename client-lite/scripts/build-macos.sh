#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DIST_DIR="$PROJECT_DIR/dist"
APP_DIR="$DIST_DIR/Easy-Net Lite.app"
CONTENTS_DIR="$APP_DIR/Contents"
VERSION="${EASY_NET_VERSION:-$(tr -d '[:space:]' < "$PROJECT_DIR/VERSION")}"
BUILD_VERSION="${EASY_NET_BUILD_VERSION:-1}"
MACOS_DEPLOYMENT_TARGET="${EASY_NET_MACOS_DEPLOYMENT_TARGET:-11.0}"

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "无效版本号：$VERSION" >&2
  exit 1
fi

if [[ ! "$MACOS_DEPLOYMENT_TARGET" =~ ^[0-9]+\.[0-9]+$ ]]; then
  echo "无效 macOS 最低版本：$MACOS_DEPLOYMENT_TARGET" >&2
  exit 1
fi

if ! xcrun --find clang >/dev/null 2>&1; then
  echo "缺少 Xcode Command Line Tools，请先运行：xcode-select --install" >&2
  exit 1
fi

mkdir -p "$CONTENTS_DIR/MacOS" "$CONTENTS_DIR/Resources"
cp "$PROJECT_DIR/build/macos/Info.plist" "$CONTENTS_DIR/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" "$CONTENTS_DIR/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $BUILD_VERSION" "$CONTENTS_DIR/Info.plist"
/usr/libexec/PlistBuddy -c "Set :LSMinimumSystemVersion $MACOS_DEPLOYMENT_TARGET" "$CONTENTS_DIR/Info.plist"

cd "$PROJECT_DIR"
export CGO_ENABLED=1
export MACOSX_DEPLOYMENT_TARGET="$MACOS_DEPLOYMENT_TARGET"
go test ./...
go build -trimpath -ldflags "-s -w -X easy-net/client-lite/internal/version.Value=$VERSION" -o "$CONTENTS_DIR/MacOS/easy-net-lite" ./cmd/easy-net

ACTUAL_DEPLOYMENT_TARGET="$(xcrun vtool -show-build "$CONTENTS_DIR/MacOS/easy-net-lite" | awk '/minos/{print $2; exit}')"
case "$ACTUAL_DEPLOYMENT_TARGET" in
  "$MACOS_DEPLOYMENT_TARGET"|"$MACOS_DEPLOYMENT_TARGET.0") ;;
  *)
    echo "macOS 二进制最低版本应为 $MACOS_DEPLOYMENT_TARGET，实际为 ${ACTUAL_DEPLOYMENT_TARGET:-未知}" >&2
    exit 1
    ;;
esac

echo "构建完成：${APP_DIR}（版本 ${VERSION}，支持 macOS ${MACOS_DEPLOYMENT_TARGET}+）"
echo "正式分发前请使用 Apple Developer ID 对 .app 签名并公证。"
