#!/bin/sh
set -eu
site="${1:?usage: verify-site.sh SITE_DIR}"
test -d "$site" && test ! -L "$site"
test -z "$(find "$site" ! -type f ! -type d -print -quit)"
for page in index.html zh/index.html 404.html robots.txt sitemap.xml \
    changelog/index.html zh/changelog/index.html license/index.html zh/license/index.html; do
    test -s "$site/$page" || { echo "missing site output: $page" >&2; exit 1; }
done
for doc in "" install/ connect/ browse/ transfers/ objects/ versions/ buckets/ links/ \
    sync/ shortcuts/ cli/ privacy/; do
    for lang in "" zh/; do
        test -s "$site/${lang}docs/${doc}index.html" || { echo "missing site output: ${lang}docs/${doc}index.html" >&2; exit 1; }
    done
done
test -n "$(find "$site/assets" -type f -name '*.css' -print -quit)"
test -n "$(find "$site/assets" -type f -name '*.js' -print -quit)"
test -n "$(find "$site/assets" -type f -name 'search-*.json' -print -quit)"
# The download mirror (tools/updates mirror): the archives of the release match
# its SHA256SUMS.
test -s "$site/download/SHA256SUMS" || { echo "missing site output: download/SHA256SUMS" >&2; exit 1; }
(cd "$site/download" && sha256sum -c --quiet --strict SHA256SUMS) || { echo "download mirror does not match SHA256SUMS" >&2; exit 1; }
