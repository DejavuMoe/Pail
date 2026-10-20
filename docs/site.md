# Landing site (pail.download)

The public website lives in this repository next to the application. It is a static site with no JavaScript framework, no Node toolchain and no runtime dependencies outside the site itself. Besides the landing page it has documentation pages that render the repository's own Markdown.

| Path | Contents | State |
| --- | --- | --- |
| `designs/pail-site/` | Reviewable prototype (landing page `index.html`, `styles.css`, `i18n.js`, `app.js`; docs pages `docs*.html`, `changelog*.html`, `license*.html` with `docs.css`, `docs.js`; `tweaks.js` is a review-only control panel) plus the prototype-first-ui records (`_d_meta.json`, `ui-contract.json`, `content-inventory.json`, `design-sources.json`) | approved 2026-10-20 |
| `site/` | Production templates, locale files and static assets | implemented |
| `tools/site/` | Go generator: `go run ./tools/site -out <dir> -version <x.y.z>` | implemented, tested |
| `.woodpecker/site.yml` | Build and deploy pipeline on Forgejo's Woodpecker | live on netcup-vps500 since 2026-10-20 (serving v0.1.0) |
| `docs/product/constraints.md` | Content and UX rules for the site | current |

Only the site uses the `prototype-first-ui` and `baoyu-design` skills (see `AGENTS.md`). A visible change starts in `designs/pail-site/`, is approved, and is then implemented in `site/` in a separate commit.

## Why one repository and a Go generator

- The site's facts (version, archive names, screenshots, icon, colors) come from this repository: `tools/release`, `docs/images/`, `assets/brand/`, `cmd/pail-desktop/theme.go`. Keeping the site here lets one commit change both.
- A Go generator needs nothing beyond the toolchain the project already pins in `go.mod` (plus the Markdown library `github.com/yuin/goldmark`, pinned in `go.mod`), so the same `golang` image builds the app and the site, and `go test ./...` covers the generator. There is no `package.json`, no lockfile and no `node_modules` for Go's `./...` patterns to trip over.
- The output is plain HTML, CSS and a small script; it can be served by any static host.

## Production layout

```text
site/
  templates/index.html.tmpl   landing page, one html/template for both languages
  templates/docs.html.tmpl    shared template of the four docs pages, both languages
  templates/404.html.tmpl     bilingual not-found page (added after the prototype; approved 2026-10-20)
  locales/en.json             source strings (keys match the prototype's i18n.js, minus its href* keys:
  locales/zh-Hans.json        the generator computes URLs; plus the not-found strings)
  static/styles.css           the approved prototype's styles without the review panel's hooks
  static/docs.css             docs pages (navigation, prose, tables, contents list)
  static/app.js               theme, mobile menu, demos; no runtime translation
  static/docs.js              highlights the current entry of "On this page"
tools/site/                   generator and tests
```

The generator (`tools/site/main.go`):

- renders `/index.html` (English) and `/zh/index.html` (Simplified Chinese) with `hreflang` alternates and a canonical URL; text is rendered on the server, and only the strings `app.js` builds at runtime (`runtimeKeys`) are embedded as JSON;
- renders the docs pages (below) and adds them to `sitemap.xml` with their language alternates;
- copies `docs/images/main-{light,dark}.webp` and `assets/brand/pail-icon.svg` (XML comments removed) instead of keeping second copies;
- renders Open Graph and Twitter large-image metadata in the initial HTML of both landing pages and every documentation page: localized title, description and image alt text, page canonical URL, `en_US` / `zh_CN` locales, and an absolute fingerprinted image URL using `-base-url`. The shared light screenshot is WebP, 1440×900; consumers may crop it. The 404 page remains `noindex` and has no social card;
- fingerprints every file under `/assets/` (`styles.<hash>.css`) so it can be cached forever while HTML is revalidated;
- writes `404.html`, `robots.txt` and `sitemap.xml`;
- takes the footer year from `LICENSE`, so the same commit and version always produce the same files;
- fails when a locale misses a key, when the two locales' key sets differ, when a string is empty, or when `-version` is not a semantic version without the `v` prefix;
- replaces an existing `-out` directory only when it holds nothing but a previous build.

Flags: `-out`, `-version` (required); `-root` (default: found by walking up to this module's `go.mod`); `-base-url` (default `https://pail.download`, used for canonical links and the sitemap).

The tests (`go test ./tools/site/...`) build the real site and check both languages and every page, the stable heading ids, that the Chinese docs reuse the English ids in order, that the six download links match the targets in `tools/release`, that every internal link and fragment resolves to a built file and id and that external links stay on the allowed hosts, that `app.js` only asks for embedded strings, fingerprinting, deterministic output, that a translation with different headings or a link that leaves the repository fails the build, and input validation.

Behaviour carried over from the prototype: theme follows the system with a System / Light / Dark control stored in `localStorage` (applied before first paint), the language button links between `/` and `/zh/` and stores the choice, a first visit to `/` with a Chinese browser language goes to `/zh/` unless a choice is stored, a disclosure menu for narrow screens, and reduced-motion support. There is no site search or command palette. Without JavaScript every section, link and download still works; the theme control, menu button and object-key backdrop are hidden, the mobile navigation is shown as a plain list on narrow screens, and the terminal shows its first example.

The site loads no webfonts. Text uses each operating system's default sans-serif and monospace stacks with CJK fallbacks (see `docs/product/constraints.md`), so the prototype and production make no third-party requests and there are no font licences to ship.

Documentation screenshots are rendered from the actual desktop UI by `TestShellRendersKeyTexts` and `TestDarkModeRenders` with `PAIL_UI_SNAPSHOT_DIR` pointing to a temporary output directory. Compress the resulting PNGs at https://picforge.de/?tool=compression, select WebP and keep the defaults (quality 75, resize off), then save them under `docs/images/`. These are controlled UI renders, not native OS window captures. The light screenshot is also the approved social preview; its dimensions must match the metadata and are checked by `TestSocialMetadata`. Local HTML/asset checks do not prove a social platform has refreshed its cache.

Social fields follow the [Open Graph protocol](https://ogp.me/); canonical and language alternates continue to identify the individual page. Metadata is rendered at build time, so crawlers do not need JavaScript.

## Documentation pages

The docs pages render the repository's own Markdown; they have no separately written copy. Only the page chrome (titles, navigation, labels) comes from the locale files.

| Page | English URL | Chinese URL | Source (en / zh-Hans) |
| --- | --- | --- | --- |
| Get started | `/docs/` | `/zh/docs/` | `README.md` / `README.zh-CN.md`, the sections Install, Quick start, Command line, Build from source, Data and privacy, Platform status (ids `install`, `quick-start`, `command-line`, `build-from-source`, `data-and-privacy`, `platform-status`) |
| Command line | `/docs/cli/` | `/zh/docs/cli/` | `docs/cli.md` / `docs/cli.zh-CN.md` |
| Changelog | `/changelog/` | `/zh/changelog/` | `CHANGELOG.md` / `CHANGELOG.zh-CN.md` |
| License | `/license/` | `/zh/license/` | `LICENSE` on both; the Chinese page shows a note and marks the text `lang="en"` |

Rules of the generator (`tools/site/docs.go`, `markdown.go`):

- Markdown is converted with `github.com/yuin/goldmark` (pinned in `go.mod`) with the GFM table extension only. Raw HTML and images in a source fail the build; they are never passed through or dropped silently.
- The README sections are chosen by position and must carry the expected titles; a renamed or reordered README section fails the build. The `# ` title and the README language-switcher line are removed; the page has its own title and language control.
- Heading ids are ASCII slugs of the English headings (`[Unreleased]` loses its brackets); the Get started ids are fixed. Chinese pages reuse the English ids by position, and the build fails when the heading count or levels differ from the English source. Only `##` and `###` headings are allowed.
- Link rewriting: a link to a file that is a docs source (`README*.md`, `docs/cli*.md`, `CHANGELOG*.md`, `LICENSE`) opens the page in the current language (fragment kept), and link text that is that path becomes the page title; any other repository path opens `https://github.com/DejavuMoe/Pail/blob/master/<path>`; absolute `http(s)`/`mailto` links are kept; a path that leaves the repository fails the build.
- Tables are wrapped in a labelled, focusable `table-wrap`; code blocks are focusable; a contents list ("On this page") appears with three or more `##` sections.
- Each page has a canonical URL and `hreflang` alternates (`en`, `zh-Hans`, `x-default`). Adding, renaming or moving a source file means changing `docPages` in `tools/site/docs.go`; the workflow builds on every `master` push.

## Version and download links

Download buttons point to GitHub Releases:

```text
https://github.com/DejavuMoe/Pail/releases/download/v<version>/Pail-v<version>-<os>-<arch>.<zip|tar.gz>
https://github.com/DejavuMoe/Pail/releases/download/v<version>/SHA256SUMS
```

The archive names are the ones `tools/release` produces; if the tool's naming changes, the generator must change with it. The tag format `v<version>` is an assumption the release CI must keep (see `docs/ci.md`).

The pipeline reads the latest published stable GitHub Release, verifies that all six versioned archive assets and `SHA256SUMS` are present and uploaded, and checks that the checksum file is publicly downloadable. It never selects an unpublished Git tag. Before the first public release it fails without changing the deployed site. After publishing a new release, manually run the website workflow from `master` to update the download version.

## Pipeline (`.woodpecker/site.yml`)

Both `master` push and manual events build and deploy the site. Tag events do not: release asset creation is asynchronous. There is no GitHub website deployment job or GitHub-to-Woodpecker callback token.

The required agent labels are `platform=linux/amd64`, `backend=docker`, `role=netcup-vps500`, `server=netcup-vps500`. The repository must be trusted for host volumes. The Go builder and Alpine publisher are pinned to image digests; Go module/build caches live under `/var/cache/woodpecker/pail/` on that agent.

1. `scripts/build-site.sh` resolves the published version, checks release assets, runs generator tests and builds `public/`.
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

Configure the web server's root as `/var/www/pail.download/html`. Serve `/zh/` from `zh/index.html`, return `404.html` with status 404, send `Cache-Control: public, max-age=31536000, immutable` for fingerprinted assets and `no-cache` for HTML. DNS, TLS, HSTS and server configuration are managed separately; a successful local deployment fixture is not evidence that production is live.

Live check on 2026-10-20: `https://pail.download/`, `/zh/` and `/docs/` return 200 and show v0.1.0; a missing path returns 404; HTML is sent with `Cache-Control: no-cache`, fingerprinted assets with `public, max-age=31536000, immutable`, and HSTS is `max-age=31536000; includeSubDomains; preload`. Repeat these checks after changing the server configuration.

Run `sh scripts/test-publish-site.sh` on Linux to check atomic activation, two-release retention, stale-run rejection and preservation of the previous site when validation fails. `SITE_DEPLOY_ROOT` overrides `/deploy` for isolated fixtures. The scripts resolve repository paths from their own location.

## Preview

Prototype: serve `designs/` and open `http://127.0.0.1:4311/pail-site/`.

```powershell
python -m http.server 4311 --bind 127.0.0.1 --directory designs
```

Production:

```powershell
go run ./tools/site -out build/site -version 0.1.0
python -m http.server 4312 --bind 127.0.0.1 --directory build/site
```

`python -m http.server` does not send `404.html` for missing paths; open `/404.html` directly to check it. Download links point to the real release, so they fail until that version is published.
