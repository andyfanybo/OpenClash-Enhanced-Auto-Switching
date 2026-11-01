#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
stage="$(mktemp -d)"
trap 'rm -r "$stage"' EXIT INT TERM
for binary in dist/node-guard-linux-*; do
    arch="${binary##*node-guard-linux-}"
    mkdir -p "$stage/dist"
    cp "$binary" "$stage/dist/node-guard"
    cp -R packaging luci docs "$stage/"
    cp config.example.json LICENSE THIRD_PARTY_NOTICES.md "$stage/"
    tar -czf "dist/openclash-ff-linux-$arch.tar.gz" -C "$stage" .
done
cd dist
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum openclash-ff-linux-*.tar.gz > SHA256SUMS
else
    shasum -a 256 openclash-ff-linux-*.tar.gz > SHA256SUMS
fi
