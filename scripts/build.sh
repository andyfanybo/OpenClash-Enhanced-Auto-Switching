#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p dist
for target in amd64 386 arm64 armv6 armv7 mips mipsle; do
    arch="$target"
    arm=7
    case "$target" in armv6) arch=arm; arm=6;; armv7) arch=arm; arm=7;; esac
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" GOMIPS=softfloat \
        go build -trimpath -ldflags='-s -w' -o "dist/node-guard-linux-$target" .
done
cp LICENSE THIRD_PARTY_NOTICES.md dist/
cd dist
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum node-guard-linux-* > SHA256SUMS
else
    shasum -a 256 node-guard-linux-* > SHA256SUMS
fi
