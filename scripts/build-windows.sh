#!/bin/bash
# Cross-compiles MyVpn for Windows (amd64 and arm64). No CGO needed: the Wails
# Windows backend uses WebView2 through pure Go. Bundles wintun.dll.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=$(grep -o 'Version = "[^"]*"' internal/ipc/ipc.go | cut -d'"' -f2)
WAILS=${WAILS:-$(go env GOPATH)/bin/wails}
WINTUN_SHA=07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
OUT=dist; mkdir -p "$OUT" build/windows-deps
if [ ! -d build/windows-deps/wintun ]; then
  curl -sSLo build/windows-deps/wintun.zip https://www.wintun.net/builds/wintun-0.14.1.zip
  echo "$WINTUN_SHA  build/windows-deps/wintun.zip" | shasum -a 256 -c -
  (cd build/windows-deps && unzip -oq wintun.zip)
fi
for ARCH in ${ARCHS:-amd64 arm64}; do
  echo "==> windows/$ARCH"
  "$WAILS" build -platform windows/$ARCH -clean -skipbindings -trimpath -ldflags "-s -w" -webview2 download -o MyVpn.exe
  CGO_ENABLED=0 GOOS=windows GOARCH=$ARCH go build -trimpath -ldflags "-s -w" -o build/bin/myvpnd.exe ./cmd/myvpnd
  CGO_ENABLED=0 GOOS=windows GOARCH=$ARCH go build -trimpath -ldflags "-s -w -H windowsgui" -o build/bin/MyVpnTray.exe ./cmd/myvpntray
  cp build/windows-deps/wintun/bin/$ARCH/wintun.dll build/bin/
  cp build/windows-deps/wintun/LICENSE.txt build/bin/wintun-LICENSE.txt
  cp docs/LEEME-windows.txt build/bin/LEEME.txt
  rm -f "$OUT/MyVpn-$VERSION-windows-$ARCH.zip"
  rm -f ./*.syso   # Wails leaves a Windows resource object here; it would break macOS builds
  (cd build/bin && zip -q -r "../../$OUT/MyVpn-$VERSION-windows-$ARCH.zip" MyVpn.exe MyVpnTray.exe myvpnd.exe wintun.dll wintun-LICENSE.txt LEEME.txt)
done
ls -lh "$OUT"/MyVpn-$VERSION-windows-*
