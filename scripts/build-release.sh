#!/usr/bin/env bash
# 交叉编译多平台并打包为开箱即用的发布压缩包。
# 用法: bash scripts/build-release.sh [版本号]   例如: bash scripts/build-release.sh v1.0.8
#
# 产物在 dist/ 下：
#   vertex-proxy-windows-amd64.zip   (Windows x64)
#   vertex-proxy-windows-386.zip     (Windows 32 位 / 老机器)
#   vertex-proxy-linux-amd64.zip     (Linux x86_64)
#   vertex-proxy-linux-386.zip       (Linux 32 位 / 老机器)
#   vertex-proxy-linux-arm64.zip     (Linux ARM 64 位 / 树莓派 3/4/5 64 位系统)
#   vertex-proxy-linux-arm32.zip       (Linux ARM 32 位 / 树莓派 0/1/2/3 32 位系统)
#   vertex-proxy-android-arm64.zip   (Android / Termux / 鸿蒙 4.x 老版本)
#   vertex-proxy-darwin-amd64.zip    (Mac Intel)
#   vertex-proxy-darwin-arm64.zip    (Mac Apple Silicon)
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-dev}"
OUT="dist"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.buildCommit=${COMMIT} -X main.buildTime=${BUILD_TIME}"

rm -rf "$OUT"
mkdir -p "$OUT"

# 平台清单： GOOS GOARCH 二进制名 压缩包名 附带的启动脚本…
build() {
  local goos="$1" goarch="$2" bin="$3" pkg="$4"; shift 4
  local stage="$OUT/$pkg"
  echo "==> 编译 $goos/$goarch"

  if [ "$goos" = "android" ]; then
    # 启用 CGO 并设置目标为 Android (arm64 架构)
    # 指定 NDK 编译器（此处使用 API 28 以兼容 Android 9+）
    if [ -z "${ANDROID_NDK_HOME:-}" ]; then
      echo "错误：未指定 ANDROID_NDK_HOME 环境变量！" >&2
      exit 1
    fi

    local host_os
    case "$(uname -s)" in
      Linux*)               host_os="linux-x86_64";;
      Darwin*)              host_os="darwin-x86_64";;
      MSYS*|MINGW*|CYGWIN*) host_os="windows-x86_64";;
      *)                    host_os="linux-x86_64";;
    esac

    local ext=""
    if [ "$host_os" = "windows-x86_64" ]; then
      ext=".cmd"
    fi

    local clang_cc="${ANDROID_NDK_HOME}/toolchains/llvm/prebuilt/${host_os}/bin/aarch64-linux-android28-clang${ext}"
    if [ ! -f "${clang_cc}" ]; then
      echo "错误：找不到 Android NDK 编译器：${clang_cc}" >&2
      exit 1
    fi

    echo "    -> 使用 NDK 编译器: ${clang_cc}"
    CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" CC="${clang_cc}" \
      go build -tags with_utls -trimpath -ldflags="$LDFLAGS" -o "$stage/$bin" ./cmd/vproxy
  else
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -tags with_utls -trimpath -ldflags="$LDFLAGS" -o "$stage/$bin" ./cmd/vproxy
  fi

  mkdir -p "$stage/config"
  cp config/config.example.json   "$stage/config/"
  cp config/api_keys.example.txt  "$stage/config/"
  cp config/models.json           "$stage/config/"
  cp 部署指南.md                   "$stage/"
  cp cmd/vproxy/rules.txt         "$stage/"  # 规则文件（已嵌入二进制，此为可读副本）
  # 附带的启动脚本/服务文件（按平台传入）
  for f in "$@"; do cp "$f" "$stage/"; done

  # 优先用 zip，没有则退回 7z（Windows 装了 7-Zip 即可）
  if command -v zip >/dev/null 2>&1; then
    (cd "$stage" && zip -rq "../$pkg.zip" ./* && cd .. && rm -rf "$pkg")
  elif command -v 7z >/dev/null 2>&1; then
    (cd "$stage" && 7z a -tzip -mx=9 "../$pkg.zip" ./* >/dev/null && cd .. && rm -rf "$pkg")
  else
    echo "错误：找不到 zip 也没有 7z，无法打包" >&2
    exit 1
  fi
  echo "    -> $OUT/$pkg.zip"
}

# Windows（附带 setup.bat 一键部署脚本）
build windows amd64 vertex-proxy.exe vertex-proxy-windows-amd64 scripts/启动.bat scripts/setup.bat
build windows 386   vertex-proxy.exe vertex-proxy-windows-386   scripts/启动.bat scripts/setup.bat

# Linux（附带 setup.sh 一键部署脚本）
build linux   amd64 vertex-proxy     vertex-proxy-linux-amd64   scripts/start.sh scripts/vertex-proxy.service scripts/setup.sh
build linux   386   vertex-proxy     vertex-proxy-linux-386     scripts/start.sh scripts/vertex-proxy.service scripts/setup.sh
build linux   arm64 vertex-proxy     vertex-proxy-linux-arm64   scripts/start.sh scripts/vertex-proxy.service scripts/setup.sh
build linux   arm32 vertex-proxy     vertex-proxy-linux-arm32   scripts/start.sh scripts/vertex-proxy.service scripts/setup.sh

# Android（启用 CGO 编译，指定 NDK 编译器，API 28 以兼容 Android 9+）
if [ -n "${ANDROID_NDK_HOME:-}" ]; then
  build android arm64 vertex-proxy     vertex-proxy-android-arm64 scripts/start.sh scripts/setup.sh
elif [ "${BUILD_ANDROID:-auto}" = "1" ] || [ "${BUILD_ANDROID:-auto}" = "true" ]; then
  echo "error: BUILD_ANDROID is enabled but ANDROID_NDK_HOME is not set" >&2
  exit 1
else
  echo "==> skip android/arm64: ANDROID_NDK_HOME is not set"
fi

# macOS（附带 setup.sh）
build darwin  amd64 vertex-proxy     vertex-proxy-darwin-amd64  scripts/start.sh scripts/setup.sh
build darwin  arm64 vertex-proxy     vertex-proxy-darwin-arm64  scripts/start.sh scripts/setup.sh

echo "完成。产物："
ls -1 "$OUT"
