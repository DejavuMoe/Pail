# Landing site (pail.download)

The public website lives in this repository next to the application. It is a static site with no JavaScript framework, no Node toolchain and no runtime dependencies outside the site itself.

| Path | Contents | State |
| --- | --- | --- |
| `designs/pail-site/` | Reviewable prototype (`index.html`, `styles.css`, `i18n.js`, `app.js`; `tweaks.js` is a review-only control panel) plus the prototype-first-ui records (`_d_meta.json`, `ui-contract.json`, `content-inventory.json`, `design-sources.json`) | waiting for approval |
| `site/` | Production templates, locale files and static assets | written after the prototype is approved |
| `tools/site/` | Go generator: `go run ./tools/site -out <dir> -version <x.y.z>` | written after the prototype is approved |
| `.woodpecker/site.yml` | Build and deploy pipeline on Forgejo's Woodpecker | written; runs once `tools/site` exists |
| `docs/product/constraints.md` | Content and UX rules for the site | current |

Only the site uses the `prototype-first-ui` and `baoyu-design` skills (see `AGENTS.md`). A visible change starts in `designs/pail-site/`, is approved, and is then implemented in `site/` in a separate commit.

## Why one repository and a Go generator

- The site's facts (version, archive names, screenshots, icon, colors) come from this repository: `scripts/release.ps1`, `docs/images/`, `assets/brand/`, `cmd/pail-desktop/theme.go`. Keeping the site here lets one commit change both.
- A Go generator needs nothing beyond the toolchain the project already pins in `go.mod`, so the same `golang` image builds the app and the site, and `go test ./...` covers the generator. There is no `package.json`, no lockfile and no `node_modules` for Go's `./...` patterns to trip over.
- The output is plain HTML, CSS and a small script; it can be served by any static host.

## Production layout (planned)

```text
site/
  templates/index.html.tmpl   one html/template for both languages
  locales/en.json             source strings (keys match the prototype's i18n.js)
  locales/zh-Hans.json
  static/                     styles.css, app.js, fonts/*.woff2 with their OFL texts
tools/site/                   generator and tests
```

The generator:

- renders `/index.html` (English) and `/zh/index.html` (Simplified Chinese) with `hreflang` alternates and a canonical URL;
- copies `docs/images/main-{light,dark}.png` and `assets/brand/pail-icon.svg` instead of keeping second copies;
- fingerprints static files (`styles.<hash>.css`) so they can be cached forever while HTML is revalidated;
- writes `404.html`, `robots.txt` and `sitemap.xml`;
- fails when a locale misses a key, when the two locales' key sets differ, or when `-version` does not match `^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`.

Behaviour carried over from the prototype: theme follows the system with a System / Light / Dark control stored in `localStorage` (applied before first paint), the language button links between `/` and `/zh/` and stores the choice, a first visit to `/` with a Chinese browser language goes to `/zh/` unless a choice is stored, the command palette (`Ctrl/⌘ K`), and reduced-motion support. Without JavaScript every section, link and download still works.

Fonts (Bricolage Grotesque, Instrument Sans, Instrument Serif, Geist Mono; all SIL OFL 1.1) are self-hosted. The prototype loads them from Google Fonts only for review; production makes no third-party requests.

## Version and download links

Download buttons point to GitHub Releases:

```text
https://github.com/DejavuMoe/Pail/releases/download/v<version>/Pail-<version>-<os>-<arch>.<zip|tar.gz>
https://github.com/DejavuMoe/Pail/releases/download/v<version>/SHA256SUMS
```

The archive names are the ones `scripts/release.ps1` produces; if the script's naming changes, the generator must change with it. The tag format `v<version>` is an assumption the release CI must keep (see `docs/ci.md`).

The pipeline takes the version from the newest `v*` tag and refuses to deploy until that release's `SHA256SUMS` is downloadable, so the site never links to archives that are not published yet. After a release workflow finishes, run the site pipeline manually (or push to `site/`) to pick the new version up.

## Pipeline (`.woodpecker/site.yml`)

| Trigger | When |
| --- | --- |
| push to `main` | only when `site/`, `tools/site/`, `docs/images/`, the brand icon or the pipeline file changed |
| tag `v*` | rebuilds with the new version (fails until the release assets exist; rerun manually) |
| manual | any time, from `main` |

Steps:

1. **build** (`golang:1.27`): `go test ./tools/site/...`, check that the release's `SHA256SUMS` exists, then `go run ./tools/site -out public -version <tag without v>`.
2. **deploy** (`alpine`): `rsync --delete --delay-updates` of `public/` to the web server over SSH.

Woodpecker secrets for the deploy step (repository secrets, available to the `push`, `tag` and `manual` events only):

| Secret | Value |
| --- | --- |
| `site_deploy_host` | SSH host of the web server |
| `site_deploy_user` | SSH user that owns the web root |
| `site_deploy_path` | absolute web root, for example `/srv/pail.download` |
| `site_deploy_key` | private key (ed25519) authorised for that user only |
| `site_deploy_known_hosts` | the server's `known_hosts` line(s); host keys are never accepted blindly |

Web server expectations: serve `/zh/` from `zh/index.html`, return `404.html` with status 404, send `Cache-Control: public, max-age=31536000, immutable` for fingerprinted files and `no-cache` for HTML, and serve over HTTPS with HSTS. DNS and TLS for pail.download are configured outside this repository.

The deploy target is still open: rsync over SSH is written in the pipeline because it works with any static web server. Switching to an object-storage bucket or a hosted static service changes only the deploy step.

## Preview

Prototype: serve `designs/` and open `http://127.0.0.1:4311/pail-site/`.

```powershell
python -m http.server 4311 --bind 127.0.0.1 --directory designs
```

Production, once `tools/site` exists:

```powershell
go run ./tools/site -out build/site -version 0.1.0
python -m http.server 4312 --bind 127.0.0.1 --directory build/site
```
