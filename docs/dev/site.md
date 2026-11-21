# Landing site (pail.download)

The public website lives in this repository next to the application. It is a static site with no JavaScript framework, no Node toolchain and no runtime dependencies outside the site itself. Besides the landing page it has documentation pages that render the repository's own Markdown.

| Path | Contents | State |
| --- | --- | --- |
| `site/` | Production templates, locale files and static assets | implemented |
| `tools/site/` | Go generator: `go run ./tools/site -out <dir> -version <x.y.z>` | implemented, tested |
| `.woodpecker/site.yml` | Build and deploy pipeline on Forgejo's Woodpecker | deployed on netcup-vps500 |
| `docs/product/constraints.md` | Content and UX rules for the site | current |

Change the site directly in `site/` and `tools/site/`.

## Why one repository and a Go generator

- The site's facts (version, archive names, screenshots, icon, docs) come from this repository: `tools/release`, `docs/images/`, `assets/brand/`, `docs/guide/`. Keeping the site here lets one commit change both. The site's colours are its own and do not follow `cmd/pail-desktop/theme.go`.
- A Go generator needs nothing beyond the toolchain the project already pins in `go.mod` (plus the Markdown library `github.com/yuin/goldmark`, pinned in `go.mod`), so the same `golang` image builds the app and the site, and `go test ./...` covers the generator. There is no `package.json`, no lockfile and no `node_modules` for Go's `./...` patterns to trip over.
- The output is plain HTML, CSS and a small script; it can be served by any static host.

## Production layout

```text
site/
  templates/index.html.tmpl   landing page, one html/template for both languages
  templates/docs.html.tmpl    every docs page, both languages
  templates/chrome.html.tmpl  head, icons, header, footer and search palette shared by all pages
  templates/panes.html.tmpl   the feature views, shared by the landing page and the guide pages
  templates/social.html.tmpl  Open Graph and Twitter metadata
  templates/404.html.tmpl     bilingual not-found page
  locales/en.json             source strings; the generator computes URLs
  locales/zh-Hans.json
  static/site.css             tokens, header, footer, palette and the landing page
  static/docs.css             docs pages (navigation, prose, tables, "On this page")
  static/site.js              every page: theme, language, menu, copy buttons, "On this page", search palette
  static/home.js              landing page: the point-cloud pail and its turn into the window, the scroll-driven feature views, the feature filter, the terminal, the download picker
tools/site/                   generator and tests
```

The footer has the logo, one line on what Pail is, three columns of links and the trademark notice. It has no personal credit and no copyright year.

The generator (`tools/site/main.go`):

- renders `/index.html` (English) and `/zh/index.html` (Simplified Chinese) with `hreflang` alternates and a canonical URL; text is rendered on the server, and only the strings the scripts set at runtime (`runtimeKeys`: `heroDownloadFor`, `sourceGithubNote`, `sourceMirrorNote`, `copyCode`, `copiedCode`, `searchEmpty`, `searchPageHit`) are embedded as JSON, and so is the recording of the terminal (`site/cli-demo.json`; the page itself shows the first example as plain HTML);
- renders the docs pages (below), adds them to `sitemap.xml` with their language alternates and writes a search index per language (`/assets/search-<lang>.<hash>.json`: page titles and `##` / `###` headings);
- copies `docs/images/main-{light,dark}-{en,zh}.webp`, `docs/images/social.webp` and `assets/brand/pail-icon.svg` (XML comments removed) instead of keeping second copies;
- renders Open Graph and Twitter large-image metadata in the initial HTML of both landing pages and every documentation page: localized title, description and image alt text, page canonical URL, `en_US` / `zh_CN` locales, and an absolute fingerprinted URL of `social.jpg` built from `-base-url`. The build re-encodes `docs/images/social.webp` as JPEG (quality 88), because not every platform reads WebP cards; it is 1200×630, and `TestSocialMetadata` checks the declared size. The 404 page stays `noindex` and has no social card;
- renders schema.org JSON-LD in the initial HTML (`tools/site/seo.go`): on both landing pages a `WebSite`, a `SoftwareApplication` (free offer, platforms, version, release date from `CHANGELOG.md`, GitHub Release, changelog page, screenshot) on every docs page a two-level `BreadcrumbList`;
- links an `apple-touch-icon` (`assets/brand/generated/pail-256.png`) and the GitHub releases Atom feed from every page;
- writes the IndexNow key file `/<indexNowKey>.txt`; landing and docs pages also send `robots: max-image-preview:large`. `TestStructuredData` checks both;
- fingerprints every file under `/assets/` (`site.<hash>.css`) so it can be cached forever while HTML is revalidated;
- writes `404.html`, `robots.txt` and `sitemap.xml` (every entry lists its `en`, `zh-Hans` and `x-default` alternates);
- shows the release date in the download section, read from the `## [x.y.z] - YYYY-MM-DD` heading of `CHANGELOG.md`; without such a heading (a preview of an unreleased version) the date is omitted, so the same commit and version always produce the same files;
- fails when a locale misses a key, when the two locales' key sets differ, when a string is empty, or when `-version` is not a semantic version without the `v` prefix;
- replaces an existing `-out` directory only when it holds nothing but a previous build.

Flags: `-out`, `-version` (required); `-root` (default: found by walking up to this module's `go.mod`); `-base-url` (default `https://pail.download`, used for canonical links and the sitemap); `-mirror-url` (default `https://pail.download/download/`, below).

### Download mirror

`-mirror-url` is the https directory of the download mirror, which holds the files of one release under their GitHub names. The pages always get `data-mirror-base=<directory>/` on the download list, a GitHub / Mirror source switch, and a plain link to the mirror directory for visitors without JavaScript. `home.js` applies the switch to every download link and stores the choice in `localStorage` under `pail-source`. `tools/site` does not check the mirror; the pipeline fills it (below). `-mirror-url ""` builds pages that offer GitHub only and show no switch.

The mirror is part of the site output. After generating the pages, `scripts/build-site.sh` runs

```sh
go run ./tools/updates mirror -version "$version" -out public -cache "$PAIL_RELEASE_CACHE"
```

which downloads the release's files from GitHub into the cache directory `v<version>/`, checks the user archives against `SHA256SUMS` and the update files against their manifests and the signatures of `internal/selfupdate.PublicKey`, and then writes:

- `public/download/`: the user archives and `SHA256SUMS`;
- `public/update/`: the update archives, delta updates and `update-<os>-<arch>.json`, with every URL in the manifests rewritten to `https://pail.download/update/<file>`. The signatures cover the files, not the manifests, so the rewrite needs no key.

Only the latest stable release is kept: the cache drops every other version, and a prerelease is refused. A release made before 0.7.0 has no update files; then `/update/` is empty and only the downloads are mirrored. A cached version that was completely downloaded is not fetched again, so a rebuild of the same version needs no GitHub downloads. `scripts/verify-site.sh` fails when `download/SHA256SUMS` is missing or does not match the archives, and the publish step runs it again on the copy it activates.

The desktop app and `pail update` read `https://pail.download/update/` when the user picks pail.download as the source, and fall back to it when GitHub cannot be reached. Because the mirror follows the site, it moves to a new release only with the next `master` push after that release is public (see [ci.md](ci.md#release-sequence)).

The tests (`go test ./tools/site/...`) build the real site and check both languages and every page, the stable heading ids, that the Chinese docs reuse the English ids in order, that the six download links match the targets in `tools/release` and that each system's row marks one usual archive, the mirror links and switch, the release date, that every internal link and fragment resolves to a built file and id and that external links stay on the allowed hosts, that the scripts only ask for embedded strings, fingerprinting, deterministic output, that a translation with different headings or a link that leaves the repository fails the build, input validation, and the writing rules in [writing.md](writing.md) (`TestCopyRules`, in `tools/site/copy_test.go`).

Behaviour of the page scripts: theme follows the system with a System / Light / Dark control stored in `localStorage` (applied before first paint), the language button links between `/` and `/zh/` and stores the choice, a first visit to `/` with a Chinese browser language goes to `/zh/` unless a choice is stored, a disclosure menu for narrow screens, a docs page list that folds into a sticky bar under the header on narrow screens, and reduced-motion support that also applies when the system setting changes while the page is open. The search button (and Ctrl K, ⌘K or `/`) opens a dialog that searches the docs index of the page's language. On the visitor's system the download table marks that row and its usual archive (x64 on Windows and Linux, Apple silicon on macOS), and the hero button downloads that archive. The pail in the hero is a canvas of points built from the icon's three strata and its handle; it assembles once, turns slowly, leans toward the pointer, and sample object keys fall into it, each adding a `PUT` line to the log under it. The four regions of the screenshot are named in plain text under it; the screenshot always fits the width of the page. The canvas of the pail lies in the page from the top of the hero to the foot of the screenshot; while the page scrolls from one to the other, each point moves to its place on the outline of the window in the screenshot (the frame, the sidebar and inspector edges, the toolbar, transfers and status lines, as fractions of the capture), and the screenshot fades in over the outline. Past the screenshot nothing is drawn. If the screenshot is already on screen before any scrolling, the pail does not change. The five features are chapters of text, each with a link to its guide page. From 1000px wide and 600px high, with scripts, their views move into one window that is `position: sticky` beside the text; the chapter at the middle of the screen is the open one, its scroll progress plays its view, and a line under the window's tabs shows that progress. The tabs are links to the chapters. The window's body takes the height of the open view and the slot it sits in the height of the tallest, so nothing around it moves. Below that size every chapter keeps its view under its text and the view plays as the chapter comes up the screen. Each view is a function of a number from 0 to 1 (`scenes` in `home.js`): it writes only what changed, so scrolling back plays it backwards, and reduced motion sets every view to 1. Scroll and resize listeners are passive and only request one frame; the positions the views need are measured on resize, not while scrolling. The page does not intercept the wheel, touch or keys. The same views open the guide pages of those features (`paneForPage` in `tools/site/docs.go`), still, and following a chapter's link carries the window across with a view transition. The other features are a list with a filter field; each entry links to its section of the guide (`moreTargets`), and the three guide pages to start with are listed before the downloads (`learnPages`). The terminal keeps the height of its longest example at the current width, so switching examples never moves the page; on wide screens it sits beside the section text, and on phones it runs edge to edge, its example tabs wrap and every line wraps. Everything that moves on its own runs only while on screen and stops under reduced motion. Without JavaScript every section, link and download still works; the theme control, search, filter and menu button are hidden, the mobile navigation is a plain list, each feature shows its view in finished state under its text, and the terminal shows its first example.

Navigating between pages uses cross-document view transitions where the browser has them: the header and the docs navigation stay in place and only the content fades. The header is the same element with the same width (`--wrap`, 1280px) on the landing page and the docs.

The site loads no fonts. Text uses the system UI face (SF Pro and PingFang SC on Apple systems, Segoe UI Variable and Microsoft YaHei on Windows, Roboto or Noto Sans with Noto Sans CJK SC elsewhere), and code uses the system monospace face (`ui-monospace`, SF Mono, Cascadia Code, Menlo, Consolas). There are no third-party requests.

## Screenshots and social card

`docs/images/main-{light,dark}-{en,zh}.webp` are native macOS window captures (2736×1822, transparent shadow) taken in demo mode (`go run -tags pail_demo ./cmd/pail-desktop --demo`), one per theme and per language, saved as WebP with their alpha channel. The landing page shows the pair for its language and theme (a dark `<source>` for the dark scheme, switched by the theme control); `README.md` uses the English pair and `README.zh-CN.md` the Chinese pair. `TestHeroScreenshotsFollowLanguageAndTheme` checks that the four have the same size, that the width and height written in the page are the file's and that each page and README refers to its own language.

`docs/images/social.webp` is the Open Graph and Twitter card. It is `main-light-en.webp` on the page background (`#F3F1EC`, 1120 px wide, 8 px from the top): `ffmpeg -f lavfi -i color=c=0xF3F1EC:s=1200x630 -i docs/images/main-light-en.webp -filter_complex "[1]scale=1120:-1[s];[0][s]overlay=40:8:format=auto" -frames:v 1 -c:v libwebp -quality 78 docs/images/social.webp` Local HTML and asset checks do not show that a social platform has refreshed its cache.

Social fields follow the [Open Graph protocol](https://ogp.me/); canonical and language alternates continue to identify the individual page. Metadata is rendered at build time, so crawlers do not need JavaScript.

## Documentation pages

The docs pages render the repository's own Markdown; they have no separately written copy. A guide page listed in `paneForPage` shows that feature's view between its lede and its text. Only the page chrome (titles, navigation, labels) comes from the locale files.

| Group | Pages | Source (en / zh-Hans) |
| --- | --- | --- |
| Get started | Overview (`/docs/`), Install, Connect storage | `docs/guide/<page>.md` / `docs/guide/<page>.zh-CN.md` |
| Using Pail | Browse, Transfers, Objects, Versions, Buckets, Links, Sync, Shortcuts | `docs/guide/<page>.md` / `.zh-CN.md` |
| Reference | Command line (`/docs/cli/`), Data and security, Changelog (`/changelog/`), License (`/license/`) | `docs/cli.md`, `docs/guide/privacy.md`, `CHANGELOG.md`, `LICENSE` and their Chinese versions; the license is English on both |

Rules of the generator (`tools/site/docs.go`, `markdown.go`):

- Markdown is converted with `github.com/yuin/goldmark` (pinned in `go.mod`) with the GFM table extension only. Raw HTML and images in a source fail the build; they are never passed through or dropped silently.
- A guide page starts with its `# ` title and one paragraph, which becomes the page's lede and description; the rest is `##` and `###` sections.
- Heading ids are ASCII slugs of the English headings (`[Unreleased]` loses its brackets); Chinese pages reuse the English ids by position, and the build fails when the heading count or levels differ from the English source. Only `##` and `###` headings are allowed.
- Inline code spans are rendered with each space-separated token in `<span class="tok">`, so a span wraps only at its spaces.
- In `sh`, `bash`, `shell` and `powershell` blocks, a `#` comment (outside quotes, at the start of a line or after a space) is wrapped in `<span class="c">` and shown dimmed.
- Link rewriting: a link to a file that is a docs source (`docs/guide/*.md`, `docs/cli*.md`, `CHANGELOG*.md`, `LICENSE`) opens the page in the current language (fragment kept), and link text that is that path becomes the page title; any other repository path opens `https://github.com/DejavuMoe/Pail/blob/master/<path>`; absolute `http(s)`/`mailto` links are kept; a path that leaves the repository fails the build.
- Tables are wrapped in a labelled, focusable `table-wrap`; code blocks are focusable; a contents list ("On this page") appears with three or more `##` sections.
- Each page has a canonical URL and `hreflang` alternates (`en`, `zh-Hans`, `x-default`). Adding, renaming or moving a source file means changing `docPages` in `tools/site/docs.go`; the workflow builds on every `master` push.

## Version and download links

Download buttons point to GitHub Releases:

```text
https://github.com/DejavuMoe/Pail/releases/download/v<version>/Pail-v<version>-<os>-<arch>.<zip|tar.gz>
https://github.com/DejavuMoe/Pail/releases/download/v<version>/SHA256SUMS
```

The archive names are the ones `tools/release` produces; if the tool's naming changes, the generator must change with it. The tag format `v<version>` is an assumption the release CI must keep (see [ci.md](ci.md)).

The pipeline reads the latest published stable GitHub Release, verifies that all six versioned archive assets and `SHA256SUMS` are present and uploaded, and checks that the checksum file is publicly downloadable. It never selects an unpublished Git tag. If the release or any of its assets is missing, it fails without changing the deployed site. The site and the mirror move to a new version with the first `master` push after the release is public, normally the commit that records its validation. That push is part of every release; run the workflow by hand only when no such push follows.

## Pipeline (`.woodpecker/site.yml`)

Both `master` push and manual events build and deploy the site. Tag events do not: release asset creation is asynchronous. There is no GitHub website deployment job or GitHub-to-Woodpecker callback token.

The required agent labels are `platform=linux/amd64`, `backend=docker`, `role=netcup-vps500`, `server=netcup-vps500`. The repository must be trusted for host volumes. The Go builder and Alpine publisher are pinned to image digests; Go module/build caches and the release cache of the mirror (`releases`, mounted as `PAIL_RELEASE_CACHE=/cache/releases`) live under `/var/cache/woodpecker/pail/` on that agent.

1. `scripts/build-site.sh` resolves the published version, checks the release assets, runs the tests of `tools/site` and `tools/updates`, builds `public/` and fills its download mirror (above). A download or signature failure fails the build without changing the deployed site.
2. `scripts/verify-site.sh` checks both languages, all page entry points and static assets, rejecting symlinks and other special files.
3. `scripts/publish-site.sh public <commit>-<pipeline>-<rerun>` copies into a new release directory, validates it, atomically replaces `html`, verifies activation and retains only the current and immediately previous release. It takes a filesystem lock, refuses out-of-root/linked deployment paths and skips older pipelines. Workflow concurrency is also limited to one.

4. `scripts/indexnow-site.sh public` submits every URL of the sitemap to IndexNow (Bing, Yandex, Seznam, Naver) with the published key. The step has `failure: ignore`, and the script itself never fails the deployment. Google and Baidu do not use IndexNow; submit the sitemap in their webmaster tools once.

The publish step mounts `/var/www/pail.download:/deploy`; no SSH deployment secrets are needed. Expected layout:

```text
/var/www/pail.download/
  html -> releases/<commit>-<pipeline>-<rerun>
  releases/
    <current>/
    <previous>/
```

Configure the web server's root as `/var/www/pail.download/html`. Serve `/zh/` from `zh/index.html`, return `404.html` with status 404, send `Cache-Control: public, max-age=31536000, immutable` for fingerprinted assets and `no-cache` for HTML. For the mirror, send `no-cache` for the files whose names do not change between releases (`/download/SHA256SUMS`, `/update/update-<os>-<arch>.json` and the `/download/` listing, which nginx generates with `autoindex`) and `public, max-age=604800` for the versioned archives and delta updates, so a deploy takes effect at once behind Cloudflare. DNS, TLS, HSTS and server configuration are managed separately.

Run `sh scripts/test-publish-site.sh` on Linux to check atomic activation, two-release retention, stale-run rejection and preservation of the previous site when validation fails. `SITE_DEPLOY_ROOT` overrides `/deploy` for isolated fixtures. The scripts resolve repository paths from their own location.

## Preview

```powershell
go run ./tools/site -out build/site -version 0.7.0
python -m http.server 4312 --bind 127.0.0.1 --directory build/site
```

`python -m http.server` does not send `404.html` for missing paths; open `/404.html` directly to check it. Download links point to the GitHub Release of the version you pass, so they only work for a published version. The preview has no mirror files; run `go run ./tools/updates mirror -version <published version> -out build/site -cache build/release-cache` to add them. The source switch is shown by default; add `-mirror-url ""` to preview the GitHub-only page.
