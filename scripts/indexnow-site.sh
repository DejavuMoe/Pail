#!/bin/sh
# Tells IndexNow (Bing, Yandex, Seznam, Naver) which URLs of the deployed site
# to crawl again: every <loc> of SITE_DIR/sitemap.xml, with the key the build
# publishes as /<key>.txt. A failure is reported and never fails the
# deployment. Usage: indexnow-site.sh SITE_DIR [BASE_URL]
set -eu
site="${1:?usage: indexnow-site.sh SITE_DIR [BASE_URL]}"
base="${2:-https://pail.download}"
host="${base#https://}"
key_file="$(find "$site" -maxdepth 1 -type f -name '*.txt' ! -name robots.txt -print | head -n 1)"
if [ -z "$key_file" ]; then
  echo "indexnow: no key file in $site" >&2
  exit 0
fi
key="$(cat "$key_file")"
urls="$(sed -n 's#.*<loc>\(.*\)</loc>.*#"\1"#p' "$site/sitemap.xml" | paste -sd, -)"
if [ -z "$urls" ]; then
  echo "indexnow: no URLs in the sitemap" >&2
  exit 0
fi
body="{\"host\":\"$host\",\"key\":\"$key\",\"keyLocation\":\"$base/$key.txt\",\"urlList\":[$urls]}"
if command -v curl >/dev/null 2>&1; then
  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json; charset=utf-8' \
    --data "$body" --max-time 30 https://api.indexnow.org/indexnow || true)"
else
  code="$(wget -q -S -O /dev/null --header='Content-Type: application/json; charset=utf-8' --post-data="$body" \
    -T 30 https://api.indexnow.org/indexnow 2>&1 | awk '/HTTP\//{c=$2} END{print c}' || true)"
fi
case "$code" in
  200|202) echo "indexnow: submitted $(printf '%s' "$urls" | tr ',' '\n' | wc -l) URLs ($code)" ;;
  *) echo "indexnow: submission answered ${code:-nothing}; the deployment is not affected" >&2 ;;
esac
