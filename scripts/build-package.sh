#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
agent="$root/agent"
deploy="$root/deploy"
output="$root/ewelink-wol-openwrt-arm64.tar.gz"

go -C "$agent" test ./...
go -C "$agent" vet ./...

stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT HUP INT TERM

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go -C "$agent" build -trimpath -ldflags='-s -w' -o "$stage/ewelink-wol" .
cp "$root/LICENSE" "$stage/LICENSE-project.txt"

for name in \
    95-ewelink-wol advanced.lua app.htm app.js config.json controller.lua \
    ewelink-wol.init install.sh LICENSE-go.txt LICENSE-gorilla-websocket.txt \
    login.lua manage.lua status.lua test-manage.lua view_model.lua; do
    cp "$deploy/$name" "$stage/$name"
done

(cd "$stage" && sha256sum \
    95-ewelink-wol advanced.lua app.htm app.js config.json controller.lua \
    ewelink-wol ewelink-wol.init install.sh LICENSE-project.txt LICENSE-go.txt LICENSE-gorilla-websocket.txt \
    login.lua manage.lua status.lua test-manage.lua view_model.lua > SHA256SUMS)

tar -czf "$output" -C "$stage" .
printf 'Package: %s\nSHA256: ' "$output"
sha256sum "$output"
