#!/bin/sh
set -eu
cd -- "$(dirname -- "$0")/.."
metadata="$(mktemp)"
trap 'rm -f -- "$metadata"' EXIT HUP INT TERM
curl --fail --silent --show-error --location --retry 3 --max-time 60 \
    https://api.github.com/repos/DejavuMoe/Pail/releases/latest > "$metadata"
version="$(jq -er '.tag_name | select(test("^v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$")) | ltrimstr("v")' "$metadata")"
jq -e '.draft == false and .prerelease == false' "$metadata" >/dev/null
for target in windows-amd64.zip windows-arm64.zip linux-amd64.tar.gz \
    linux-arm64.tar.gz darwin-amd64.zip darwin-arm64.zip; do
    jq -e --arg name "Pail-v$version-$target" \
        '[.assets[] | select(.name == $name and .size > 0 and .state == "uploaded")] | length == 1' "$metadata" >/dev/null
done
jq -e '[.assets[] | select(.name == "SHA256SUMS" and .size > 0 and .state == "uploaded")] | length == 1' "$metadata" >/dev/null
curl --fail --silent --show-error --location --retry 3 --max-time 60 \
    "https://github.com/DejavuMoe/Pail/releases/download/v$version/SHA256SUMS" > /dev/null
go test ./tools/site/...
go run ./tools/site -out public -version "$version"
sh scripts/verify-site.sh public
