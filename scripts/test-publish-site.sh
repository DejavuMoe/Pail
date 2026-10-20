#!/bin/sh
set -eu
cd -- "$(dirname -- "$0")/.."
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT HUP INT TERM
site="$fixture/site"
deploy="$fixture/deploy"
mkdir -p "$deploy"
go run ./tools/site -out "$site" -version 0.1.0
export SITE_DEPLOY_ROOT="$deploy"
sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
sh scripts/publish-site.sh "$site" "$sha-1-0"
test "$(readlink "$deploy/html")" = "releases/$sha-1-0"
sh scripts/publish-site.sh "$site" "$sha-2-0"
sh scripts/publish-site.sh "$site" "$sha-3-0"
test "$(find "$deploy/releases" -mindepth 1 -maxdepth 1 -type d | wc -l)" -eq 2
sh scripts/publish-site.sh "$site" "$sha-1-1"
test "$(readlink "$deploy/html")" = "releases/$sha-3-0"
if sh scripts/publish-site.sh "$site" "../bad"; then
    echo "unsafe release id accepted" >&2; exit 1
fi
rm "$site/zh/index.html"
if sh scripts/publish-site.sh "$site" "$sha-4-0"; then
    echo "incomplete site accepted" >&2; exit 1
fi
test "$(readlink "$deploy/html")" = "releases/$sha-3-0"
test -s "$deploy/html/zh/index.html"
echo "Site deployment: activation, retention, stale runs and failure preservation passed"
