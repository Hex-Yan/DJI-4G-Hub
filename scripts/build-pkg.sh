#!/bin/bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:-1.0.0}

PROJECT="${ROOT_DIR}/macos/DJI4GHub/DJI4GHub.xcodeproj"
SCHEME="DJI 4G Hub"
DERIVED_DATA="${ROOT_DIR}/dist/DerivedData-PKG"
APP_SOURCE="${DERIVED_DATA}/Build/Products/Release/DJI 4G Hub.app"

BACKEND_DIR="${ROOT_DIR}/dist/release/DJOneHub-macOS-arm64-dev"

WATCHER_SOURCE="${ROOT_DIR}/macos/DJI4GHubUSBWatcher/main.swift"
WATCHER_BUILD="${ROOT_DIR}/dist/tools/dji4ghub-usb-watcher"
LAUNCH_AGENT_SOURCE="${ROOT_DIR}/packaging/launchagents/com.hexyan.dji4ghub.usbwatcher.plist"

PKG_ROOT="${ROOT_DIR}/dist/pkgroot"
PKG_DIR="${ROOT_DIR}/dist/pkg"
COMPONENT_PKG="${PKG_DIR}/DJI-4G-Hub-component.pkg"
PKG_PATH="${PKG_DIR}/DJI-4G-Hub-${VERSION}.pkg"

DISTRIBUTION="${ROOT_DIR}/packaging/Distribution.xml"
PRODUCT_RESOURCES="${ROOT_DIR}/packaging/product-resources"
PKG_SCRIPTS="${ROOT_DIR}/packaging/scripts"

echo "==> 构建后台发行包"
"${ROOT_DIR}/scripts/package-macos-arm64.sh"

echo "==> 构建 USB Watcher"
mkdir -p "$(dirname "${WATCHER_BUILD}")"

xcrun swiftc \
  "${WATCHER_SOURCE}" \
  -framework AppKit \
  -framework IOKit \
  -o "${WATCHER_BUILD}"

echo "==> 构建 Release App"
rm -rf "${DERIVED_DATA}"

xcodebuild \
  -project "${PROJECT}" \
  -scheme "${SCHEME}" \
  -configuration Release \
  -derivedDataPath "${DERIVED_DATA}" \
  CODE_SIGN_IDENTITY="-" \
  CODE_SIGNING_ALLOWED=YES \
  build

if [ ! -d "${APP_SOURCE}" ]; then
  echo "错误：未找到 App：${APP_SOURCE}" >&2
  exit 1
fi

if [ ! -x "${BACKEND_DIR}/nerv" ] || \
   [ ! -x "${BACKEND_DIR}/djonehub" ] || \
   [ ! -x "${BACKEND_DIR}/bin/djonehub-macos" ]; then
  echo "错误：后台发行包内容不完整。" >&2
  exit 1
fi

if [ ! -x "${WATCHER_BUILD}" ]; then
  echo "错误：USB Watcher 未成功构建。" >&2
  exit 1
fi

if [ ! -f "${LAUNCH_AGENT_SOURCE}" ]; then
  echo "错误：缺少 LaunchAgent：${LAUNCH_AGENT_SOURCE}" >&2
  exit 1
fi

echo "==> 组装 PKG 根目录"
rm -rf "${PKG_ROOT}"

mkdir -p \
  "${PKG_ROOT}/Applications" \
  "${PKG_ROOT}/usr/local/libexec/djonehub" \
  "${PKG_ROOT}/usr/local/bin" \
  "${PKG_ROOT}/Library/LaunchAgents" \
  "${PKG_DIR}"

ditto \
  "${APP_SOURCE}" \
  "${PKG_ROOT}/Applications/DJI 4G Hub.app"

ditto \
  "${BACKEND_DIR}" \
  "${PKG_ROOT}/usr/local/libexec/djonehub"

cp "${WATCHER_BUILD}" \
  "${PKG_ROOT}/usr/local/libexec/djonehub/dji4ghub-usb-watcher"

chmod 755 \
  "${PKG_ROOT}/usr/local/libexec/djonehub/dji4ghub-usb-watcher"

cp "${LAUNCH_AGENT_SOURCE}" \
  "${PKG_ROOT}/Library/LaunchAgents/com.hexyan.dji4ghub.usbwatcher.plist"

chmod 644 \
  "${PKG_ROOT}/Library/LaunchAgents/com.hexyan.dji4ghub.usbwatcher.plist"

ln -sfn \
  ../libexec/djonehub/nerv \
  "${PKG_ROOT}/usr/local/bin/nerv"

ln -sfn \
  ../libexec/djonehub/djonehub \
  "${PKG_ROOT}/usr/local/bin/djonehub"

echo "==> 生成组件包"
rm -f "${COMPONENT_PKG}" "${PKG_PATH}"

pkgbuild \
  --root "${PKG_ROOT}" \
  --scripts "${PKG_SCRIPTS}" \
  --identifier com.hexyan.dji4ghub \
  --version "${VERSION}" \
  --install-location / \
  "${COMPONENT_PKG}"

echo "==> 生成带欢迎页的最终 PKG"
productbuild \
  --distribution "${DISTRIBUTION}" \
  --resources "${PRODUCT_RESOURCES}" \
  --package-path "${PKG_DIR}" \
  "${PKG_PATH}"

echo
echo "PKG 构建完成："
echo "  ${PKG_PATH}"
echo
ls -lh "${PKG_PATH}"
