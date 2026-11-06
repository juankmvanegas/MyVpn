#!/bin/sh
# Builds myvpnd + fakegw for Linux and runs the TUN integration test in Docker.
set -eu
cd "$(dirname "$0")/.."
mkdir -p build/it
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/x86_64/amd64/;s/aarch64/arm64/')
GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -o build/it/myvpnd ./cmd/myvpnd
GOOS=linux GOARCH=$ARCH CGO_ENABLED=0 go build -o build/it/fakegw ./cmd/fakegw
docker run --rm --cap-add NET_ADMIN --device /dev/net/tun \
  -v "$PWD/build/it:/out:ro" -v "$PWD/scripts/linux-integration.sh:/it.sh:ro" \
  alpine:3.20 sh -c 'apk add -q iproute2 iputils >/dev/null && sh /it.sh'
