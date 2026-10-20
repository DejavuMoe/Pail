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
sums="$(mktemp)"
mirror_sums="$(mktemp)"
trap 'rm -f -- "$metadata" "$sums" "$mirror_sums"' EXIT HUP INT TERM
curl --fail --silent --show-error --location --retry 3 --max-time 60 \
    "https://github.com/DejavuMoe/Pail/releases/download/v$version/SHA256SUMS" > "$sums"

# The mirror is uploaded by hand after each release. Offer it only when it has
# every archive at the release's size and the same SHA256SUMS.
mirror="${PAIL_SITE_MIRROR-https://pan.zsh.moe/Pail}"
mirror_ok=0
if [ -n "$mirror" ]; then
    mirror_ok=1
    for target in windows-amd64.zip windows-arm64.zip linux-amd64.tar.gz \
        linux-arm64.tar.gz darwin-amd64.zip darwin-arm64.zip; do
        name="Pail-v$version-$target"
        want="$(jq -r --arg name "$name" '.assets[] | select(.name == $name) | .size' "$metadata")"
        got="$(curl --fail --silent --head --location --retry 2 --max-time 30 "$mirror/v$version/$name" |
            tr -d '\r' | awk 'tolower($1) == "content-length:" { n = $2 } END { print n }')" || got=""
        [ "$got" = "$want" ] || { mirror_ok=0; echo "mirror: $name has ${got:-no} bytes, want $want" >&2; }
    done
    if ! curl --fail --silent --location --retry 2 --max-time 30 "$mirror/v$version/SHA256SUMS" > "$mirror_sums" ||
        ! cmp -s "$sums" "$mirror_sums"; then
        mirror_ok=0
        echo "mirror: SHA256SUMS missing or different" >&2
    fi
fi
go test ./tools/site/...
if [ "$mirror_ok" = 1 ]; then
    echo "mirror: v$version complete, offering $mirror"
    go run ./tools/site -out public -version "$version" -mirror-url "$mirror"
else
    echo "mirror: not offered for v$version; downloads link to GitHub only"
    go run ./tools/site -out public -version "$version"
fi
sh scripts/verify-site.sh public
