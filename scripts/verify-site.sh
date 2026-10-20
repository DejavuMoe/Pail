#!/bin/sh
set -eu
site="${1:?usage: verify-site.sh SITE_DIR}"
test -d "$site" && test ! -L "$site"
test -z "$(find "$site" ! -type f ! -type d -print -quit)"
for page in index.html zh/index.html docs/index.html zh/docs/index.html \
    docs/cli/index.html zh/docs/cli/index.html changelog/index.html \
    zh/changelog/index.html license/index.html zh/license/index.html \
    404.html robots.txt sitemap.xml; do
    test -s "$site/$page" || { echo "missing site output: $page" >&2; exit 1; }
done
test -n "$(find "$site/assets" -type f -name '*.css' -print -quit)"
test -n "$(find "$site/assets" -type f -name '*.js' -print -quit)"
