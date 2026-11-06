#!/bin/bash
# Builds MyVpn for Linux inside Docker (the GUI needs GTK3 + WebKit2GTK 4.1).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=$(grep -o 'Version = "[^"]*"' internal/ipc/ipc.go | cut -d'"' -f2)
OUT=dist; mkdir -p "$OUT"
for ARCH in ${ARCHS:-arm64 amd64}; do
  echo "==> linux/$ARCH"
  docker run --rm --platform linux/$ARCH -v "$PWD:/src" -w /src \
    -e GOFLAGS=-buildvcs=false -e HOME=/tmp -e ARCH=$ARCH -e VERSION=$VERSION \
    golang:1.26-bookworm bash -c '
      set -e
      apt-get update -qq && apt-get install -y -qq libgtk-3-dev libwebkit2gtk-4.1-dev pkg-config >/dev/null
      go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1
      $(go env GOPATH)/bin/wails build -platform linux/$ARCH -clean -skipbindings -trimpath -tags webkit2_41 -ldflags "-s -w" -o MyVpn
      CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o build/bin/myvpnd ./cmd/myvpnd
      CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o build/bin/MyVpnTray ./cmd/myvpntray
      P=build/pkg/MyVpn-$VERSION-linux-$ARCH; rm -rf build/pkg; mkdir -p $P
      cp build/bin/MyVpn build/bin/MyVpnTray build/bin/myvpnd build/appicon.png packaging/linux/* $P/
      tar -C build/pkg -czf dist/MyVpn-$VERSION-linux-$ARCH.tar.gz MyVpn-$VERSION-linux-$ARCH
      chown -R '"$(id -u):$(id -g)"' build dist
    '
done
ls -lh "$OUT"/MyVpn-$VERSION-linux-*
