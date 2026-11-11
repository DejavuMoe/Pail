# Landing site (pail.download)

The public website lives in this repository next to the application. It is a static site with no JavaScript framework, no Node toolchain and no runtime dependencies outside the site itself. Besides the landing page it has documentation pages that render the repository's own Markdown.

| Path | Contents | State |
| --- | --- | --- |
| `designs/pail-site/` | Reviewable prototype (landing page `index.html`, `styles.css`, `i18n.js`, `app.js`; docs pages `docs*.html`, `changelog*.html`, `license*.html` with `docs.css`, `docs.js`; `social-preview.html`, the composition of the social card) plus the design records (`_d_meta.json`, `ui-contract.json`, `content-inventory.json`, `design-sources.json`) | landing page revision 2 and docs pages approved 2026-10-21; social preview approved 2026-10-21 |
| `site/` | Production templates, locale files and static assets | implemented |
| `tools/site/` | Go generator: `go run ./tools/site -out <dir> -version <x.y.z>` | implemented, tested |
| `.woodpecker/site.yml` | Build and deploy pipeline on Forgejo's Woodpecker | deployed on netcup-vps500 |
| `docs/product/constraints.md` | Content and UX rules for the site | current |

A visible change starts in `designs/pail-site/`, is approved, and is then implemented in `site/` in a separate commit.

## Why one repository and a Go generator

- The site's facts (version, archive names, screenshots, icon, colors) come from this repository: `tools/release`, `docs/images/`, `assets/brand/`, `cmd/pail-desktop/theme.go`. Keeping the site here lets one commit change both.
- A Go generator needs nothing beyond the toolchain the project already pins in `go.mod` (plus the Markdown library `github.com/yuin/goldmark`, pinned in `go.mod`), so the same `golang` image builds the app and the site, and `go test ./...` covers the generator. There is no `package.json`, no lockfile and no `node_modules` for Go's `./...` patterns to trip over.
- The output is plain HTML, CSS and a small script; it can be served by any static host.

## Production layout

```text
site/
  templates/index.html.tmpl   landing page, one html/template for both languages
  templates/docs.html.tmpl    shared template of the four docs pages, both languages
  templates/footer.html.tmpl  footer shared by the landing page and the docs pages
  templates/social.html.tmpl  Open Graph and Twitter metadata
  templates/404.html.tmpl     bilingual not-found page
  locales/en.json             source strings (keys match the prototype's i18n.js, minus its href* keys:
  locales/zh-Hans.json        the generator computes URLs; plus the not-found strings)
  static/styles.css           the approved prototype's styles
  static/docs.css             docs pages (navigation, prose, tables, contents list)
  static/app.js               theme, mobile menu, download source, demos; no runtime translation
  static/docs.js              highlights the current entry of "On this page"
tools/site/                   generator and tests
```

The footer has the logo, the navigation links and the trademark notice. It has no personal credit and no copyright year.

The generator (`tools/site/main.go`):

- renders `/index.html` (English) and `/zh/index.html` (Simplified Chinese) with `hreflang` alternates and a canonical URL; text is rendered on the server, and only the strings `app.js` builds at runtime (`runtimeKeys`: `heroDownloadFor`, `sourceGithubNote`, `sourceMirrorNote`, `miniBytesOf`) are embedded as JSON, and so is the recording of the terminal (`site/cli-demo.json`; the page itself shows the first example as plain HTML);
- renders the docs pages (below) and adds them to `sitemap.xml` with their language alternates;
- copies `docs/images/main-{light,dark}-{en,zh}.webp`, `docs/images/social.webp`, `docs/video/pail-demo.{mp4,webp}` and `assets/brand/pail-icon.svg` (XML comments removed) instead of keeping second copies;
- renders Open Graph and Twitter large-image metadata in the initial HTML of both landing pages and every documentation page: localized title, description and image alt text, page canonical URL, `en_US` / `zh_CN` locales, and an absolute fingerprinted URL of `social.webp` built from `-base-url`. `social.webp` is a 1200×630 card, and `TestSocialMetadata` checks the declared size. The 404 page stays `noindex` and has no social card;
- fingerprints every file under `/assets/` (`styles.<hash>.css`) so it can be cached forever while HTML is revalidated;
- writes `404.html`, `robots.txt` and `sitemap.xml`;
- shows the release date in the download section, read from the `## [x.y.z] - YYYY-MM-DD` heading of `CHANGELOG.md`; without such a heading (a preview of an unreleased version) the date is omitted, so the same commit and version always produce the same files;
- fails when a locale misses a key, when the two locales' key sets differ, when a string is empty, or when `-version` is not a semantic version without the `v` prefix;
- replaces an existing `-out` directory only when it holds nothing but a previous build.

Flags: `-out`, `-version` (required); `-root` (default: found by walking up to this module's `go.mod`); `-base-url` (default `https://pail.download`, used for canonical links and the sitemap); `-mirror-url` (default `https://pan.zsh.moe/Pail`, below).

### Download mirror

`-mirror-url` is the https root of the download mirror; each release is uploaded to its `v<version>/` directory under the GitHub file names. The pages always get `data-mirror-base=<root>/v<version>/` on the download list for the version they are built for, a GitHub / Mirror source switch, and a plain link to the mirror directory for visitors without JavaScript. `app.js` applies the switch to every download link and stores the choice in `localStorage` under `pail-source`. The build does not check the mirror. `-mirror-url ""` builds pages that offer GitHub only and show no switch.

The tests (`go test ./tools/site/...`) build the real site and check both languages and every page, the stable heading ids, that the Chinese docs reuse the English ids in order, that the six download links match the targets in `tools/release`, the mirror links and switch, the release date, that every internal link and fragment resolves to a built file and id and that external links stay on the allowed hosts, that `app.js` only asks for embedded strings, fingerprinting, deterministic output, that a translation with different headings or a link that leaves the repository fails the build, input validation, and the writing rules in [writing.md](writing.md) (`TestCopyRules`, in `tools/site/copy_test.go`).

Behaviour of the page scripts: theme follows the system with a System / Light / Dark control stored in `localStorage` (applied before first paint), the language button links between `/` and `/zh/` and stores the choice, a first visit to `/` with a Chinese browser language goes to `/zh/` unless a choice is stored, a disclosure menu for narrow screens, and reduced-motion support that also applies when the system setting changes while the page is open. The demo video is a native `<video>` that does not autoplay and has a poster and a plain download link inside it; its captions are part of the picture, so it has no `<track>`. There is no site search or command palette. Without JavaScript every section, link and download still works; the theme control, menu button and object-key backdrop are hidden, the mobile navigation is shown as a plain list on narrow screens, and the terminal shows its first example.

The site loads no webfonts. Text uses each operating system's default sans-serif and monospace stacks with CJK fallbacks (see `docs/product/constraints.md`), so the prototype and production make no third-party requests and there are no font licences to ship.

## Screenshots and social card

`docs/images/main-{light,dark}-{en,zh}.webp` are native macOS window captures (2684×1750, transparent shadow) taken in demo mode (`go run -tags pail_demo ./cmd/pail-desktop --demo`), one per theme and per language, saved as WebP with their alpha channel. The landing page shows the pair for its language and theme (a dark `<source>` for the dark scheme, switched by the theme control); `README.md` uses the English pair and `README.zh-CN.md` the Chinese pair. `TestHeroScreenshotsFollowLanguageAndTheme` checks that the four have the same size, that the width and height written in the page are the file's and that each page and README refers to its own language.

`docs/images/social.webp` is the Open Graph and Twitter card. It is `main-light-en.webp` on the page background (`#F3F1EC`, 1120 px wide, 8 px from the top, as in `designs/pail-site/social-preview.html`): `ffmpeg -f lavfi -i color=c=0xF3F1EC:s=1200x630 -i docs/images/main-light-en.webp -filter_complex "[1]scale=1120:-1[s];[0][s]overlay=40:8:format=auto" -frames:v 1 -c:v libwebp -quality 78 docs/images/social.webp`; copy it to `designs/pail-site/assets/`. Local HTML and asset checks do not show that a social platform has refreshed its cache.

Social fields follow the [Open Graph protocol](https://ogp.me/); canonical and language alternates continue to identify the individual page. Metadata is rendered at build time, so crawlers do not need JavaScript.

## Documentation pages

The docs pages render the repository's own Markdown; they have no separately written copy. Only the page chrome (titles, navigation, labels) comes from the locale files.

| Page | English URL | Chinese URL | Source (en / zh-Hans) |
| --- | --- | --- | --- |
| Get started | `/docs/` | `/zh/docs/` | `README.md` / `README.zh-CN.md`, the sections Install, Quick start, Command line, Data and privacy, Tested platforms and services, Build from source (ids `install`, `quick-start`, `command-line`, `data-and-privacy`, `tested`, `build-from-source`) |
| Command line | `/docs/cli/` | `/zh/docs/cli/` | `docs/cli.md` / `docs/cli.zh-CN.md` |
| Changelog | `/changelog/` | `/zh/changelog/` | `CHANGELOG.md` / `CHANGELOG.zh-CN.md` |
| License | `/license/` | `/zh/license/` | `LICENSE` on both; the Chinese page shows a note and marks the text `lang="en"` |

Rules of the generator (`tools/site/docs.go`, `markdown.go`):

- Markdown is converted with `github.com/yuin/goldmark` (pinned in `go.mod`) with the GFM table extension only. Raw HTML and images in a source fail the build; they are never passed through or dropped silently.
- The README has eight `##` sections, in this order: Features, Install, Quick start, Command line, Data and privacy, Tested platforms and services, Build from source, License (Chinese: 功能, 安装, 快速开始, 命令行, 数据与隐私, 已测试的平台与服务, 从源码构建, 许可证). Sections are chosen by position and must carry these titles; a renamed or reordered section fails the build. The Get started page shows sections 2 to 7. The `# ` title and the README language-switcher line are removed; the page has its own title and language control.
- Heading ids are ASCII slugs of the English headings (`[Unreleased]` loses its brackets); the Get started section ids are fixed. Chinese pages reuse the English ids by position, `###` subsections of the README included, and the build fails when the heading count or levels differ from the English source. Only `##` and `###` headings are allowed.
- Inline code spans are rendered with each space-separated token in `<span class="tok">`, so a span wraps only at its spaces.
- Link rewriting: a link to a file that is a docs source (`README*.md`, `docs/cli*.md`, `CHANGELOG*.md`, `LICENSE`) opens the page in the current language (fragment kept), and link text that is that path becomes the page title; any other repository path opens `https://github.com/DejavuMoe/Pail/blob/master/<path>`; absolute `http(s)`/`mailto` links are kept; a path that leaves the repository fails the build.
- Tables are wrapped in a labelled, focusable `table-wrap`; code blocks are focusable; a contents list ("On this page") appears with three or more `##` sections.
- Each page has a canonical URL and `hreflang` alternates (`en`, `zh-Hans`, `x-default`). Adding, renaming or moving a source file means changing `docPages` in `tools/site/docs.go`; the workflow builds on every `master` push.

## Version and download links

Download buttons point to GitHub Releases:

```text
https://github.com/DejavuMoe/Pail/releases/download/v<version>/Pail-v<version>-<os>-<arch>.<zip|tar.gz>
https://github.com/DejavuMoe/Pail/releases/download/v<version>/SHA256SUMS
```

The archive names are the ones `tools/release` produces; if the tool's naming changes, the generator must change with it. The tag format `v<version>` is an assumption the release CI must keep (see [ci.md](ci.md)).

The pipeline reads the latest published stable GitHub Release, verifies that all six versioned archive assets and `SHA256SUMS` are present and uploaded, and checks that the checksum file is publicly downloadable. It never selects an unpublished Git tag. If the release or any of its assets is missing, it fails without changing the deployed site. The site moves to a new version with the first `master` push after the release is public, normally the commit that records its validation; run the workflow by hand only when no such push follows.

## Pipeline (`.woodpecker/site.yml`)

Both `master` push and manual events build and deploy the site. Tag events do not: release asset creation is asynchronous. There is no GitHub website deployment job or GitHub-to-Woodpecker callback token.

The required agent labels are `platform=linux/amd64`, `backend=docker`, `role=netcup-vps500`, `server=netcup-vps500`. The repository must be trusted for host volumes. The Go builder and Alpine publisher are pinned to image digests; Go module/build caches live under `/var/cache/woodpecker/pail/` on that agent.

1. `scripts/build-site.sh` resolves the published version, checks the release assets, runs the generator tests and builds `public/` with the mirror links of that version. The pipeline does not check the mirror and the link rule never changes, so the mirror never needs another run: upload each release's archives and `SHA256SUMS` to `v<version>/` by hand once the release is published. Until they are there, the mirror links of that version fail and the GitHub links work.
2. `scripts/verify-site.sh` checks both languages, all page entry points and static assets, rejecting symlinks and other special files.
3. `scripts/publish-site.sh public <commit>-<pipeline>-<rerun>` copies into a new release directory, validates it, atomically replaces `html`, verifies activation and retains only the current and immediately previous release. It takes a filesystem lock, refuses out-of-root/linked deployment paths and skips older pipelines. Workflow concurrency is also limited to one.

The publish step mounts `/var/www/pail.download:/deploy`; no SSH deployment secrets are needed. Expected layout:

```text
/var/www/pail.download/
  html -> releases/<commit>-<pipeline>-<rerun>
  releases/
    <current>/
    <previous>/
```

Configure the web server's root as `/var/www/pail.download/html`. Serve `/zh/` from `zh/index.html`, return `404.html` with status 404, send `Cache-Control: public, max-age=31536000, immutable` for fingerprinted assets and `no-cache` for HTML. DNS, TLS, HSTS and server configuration are managed separately.

Run `sh scripts/test-publish-site.sh` on Linux to check atomic activation, two-release retention, stale-run rejection and preservation of the previous site when validation fails. `SITE_DEPLOY_ROOT` overrides `/deploy` for isolated fixtures. The scripts resolve repository paths from their own location.

## Preview

Prototype: serve the repository root and open `http://127.0.0.1:4311/designs/pail-site/`. The prototype's demo section and terminal read `docs/video/` and `cli-demo.json` by relative path.

```powershell
python -m http.server 4311 --bind 127.0.0.1 --directory .
```

Production:

```powershell
go run ./tools/site -out build/site -version 0.6.1
python -m http.server 4312 --bind 127.0.0.1 --directory build/site
```

`python -m http.server` does not send `404.html` for missing paths; open `/404.html` directly to check it. Download links point to the GitHub Release of the version you pass, so they only work for a published version. The source switch is shown by default; add `-mirror-url ""` to preview the GitHub-only page.
