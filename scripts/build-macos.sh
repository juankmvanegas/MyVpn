#!/bin/bash
# Builds MyVpn.app as a universal binary (Apple Silicon arm64 + Intel x86_64)
# with the myvpnd service embedded, and packages it as a DMG.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=$(grep -o 'Version = "[^"]*"' internal/ipc/ipc.go | cut -d'"' -f2)
WAILS=${WAILS:-$(go env GOPATH)/bin/wails}
OUT=dist; mkdir -p "$OUT"
rm -f ./*.syso

echo "==> GUI (universal)"
"$WAILS" build -platform darwin/universal -clean -skipbindings -trimpath -ldflags "-s -w" -o MyVpn

echo "==> servicio myvpnd (universal)"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o build/bin/myvpnd-arm64 ./cmd/myvpnd
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o build/bin/myvpnd-amd64 ./cmd/myvpnd
lipo -create -output build/bin/MyVpn.app/Contents/MacOS/myvpnd build/bin/myvpnd-arm64 build/bin/myvpnd-amd64
rm build/bin/myvpnd-arm64 build/bin/myvpnd-amd64

APP=build/bin/MyVpn.app

echo "==> icono de barra de menús MyVpnTray.app (universal)"
TRAY="$APP/Contents/Library/MyVpnTray.app"
mkdir -p "$TRAY/Contents/MacOS"
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o build/bin/tray-arm64 ./cmd/myvpntray
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o build/bin/tray-amd64 ./cmd/myvpntray
lipo -create -output "$TRAY/Contents/MacOS/MyVpnTray" build/bin/tray-arm64 build/bin/tray-amd64
rm build/bin/tray-arm64 build/bin/tray-amd64
cat > "$TRAY/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>MyVpnTray</string>
	<key>CFBundleIdentifier</key><string>com.myvpn.tray</string>
	<key>CFBundleName</key><string>MyVpn</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>LSMinimumSystemVersion</key><string>10.13</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier com.myvpn.app" "$APP/Contents/Info.plist" 2>/dev/null || true
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" "$APP/Contents/Info.plist" 2>/dev/null || true
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $VERSION" "$APP/Contents/Info.plist" 2>/dev/null || true

echo "==> firma"
if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
  codesign --force --options runtime --timestamp -s "$MACOS_SIGN_IDENTITY" "$APP/Contents/MacOS/myvpnd"
  codesign --force --options runtime --timestamp -s "$MACOS_SIGN_IDENTITY" "$TRAY"
  codesign --force --options runtime --timestamp -s "$MACOS_SIGN_IDENTITY" "$APP"
else
  codesign --force -s - "$APP/Contents/MacOS/myvpnd"
  codesign --force -s - "$TRAY"
  codesign --force -s - "$APP"   # firma ad-hoc (uso personal)
fi
lipo -info "$APP/Contents/MacOS/MyVpn" "$APP/Contents/MacOS/myvpnd" "$TRAY/Contents/MacOS/MyVpnTray"

echo "==> DMG"
STAGE=$(mktemp -d); cp -R "$APP" "$STAGE/"; ln -s /Applications "$STAGE/Applications"
rm -f "$OUT/MyVpn-$VERSION-macos-universal.dmg"
hdiutil create -quiet -volname "MyVpn" -srcfolder "$STAGE" -ov -format UDZO "$OUT/MyVpn-$VERSION-macos-universal.dmg"
rm -rf "$STAGE"
ditto -c -k --keepParent "$APP" "$OUT/MyVpn-$VERSION-macos-universal.zip"
ls -lh "$OUT"/MyVpn-$VERSION-macos-*
