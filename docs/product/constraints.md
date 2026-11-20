# Pail landing site — product and UX constraints

Scope: the public landing site at https://pail.download (`site/`, built by `tools/site`). The desktop app is not governed by this file; its rules are in `docs/dev/design.md`.

## Product

- Product type: static marketing site for an open-source desktop app and CLI.
- Primary users: people who manage S3-compatible storage and are deciding whether to install Pail.
- Primary jobs: understand what Pail does, download the right archive, read how to install and use it, find the source.
- Pages: the landing page and the documentation: the guide in `docs/guide/` (`*.md` and `*.zh-CN.md`), the CLI reference from `docs/cli.md` / `docs/cli.zh-CN.md`, the changelog from `CHANGELOG.md` / `CHANGELOG.zh-CN.md` and the license, in English only because the English text is the binding one. Only the open-source project itself — repository, source files, issues, releases and download assets — links to GitHub.
- UI surface classification: marketing.

## Supported environments

- Current Chrome, Edge, Firefox and Safari; must stay usable without JavaScript (content, links and downloads work; the theme toggle, mobile menu, search, the feature filter, the point-cloud pail, the feature window and the terminal are enhancements, and each feature view shows its finished state under its text).
- Widths from 360px to 1920px with no horizontal page scroll; 16px minimum side gutter.
- Keyboard, pointer and touch. `prefers-reduced-motion` stops every animation; the pail is drawn once, still. Anything that moves on its own runs only while on screen, and the feature tour stops advancing on the first click, on hover and on focus.
- English at `/`, Simplified Chinese at `/zh/`. Long Chinese and English strings must not clip.
- No third-party requests at runtime: images are self-hosted, no font files are loaded, and there is no analytics.

## Must preserve

- The site has its own palette and does not follow `cmd/pail-desktop/theme.go`: a near-black ground in dark, white in light, one blue accent, the three blues of the icon's strata for the pail and the feature views. The terminal is dark in both themes; code blocks have a light sheet in the light theme and a dark slate in the dark theme, never pure black.
- Light and dark follow the system by default; one button in the header steps through System, Light and Dark and the choice persists.
- Selected, current and on states use at least two cues.
- Download links point to GitHub Releases assets named by `tools/release`. The mirror at https://pan.zsh.moe/Pail/v<version>/, with the same file names, is always offered as a second source for the same version.
- No analytics, trackers, cookies or external embeds.

## Explicit non-goals

- No blog, account, pricing or newsletter. Search covers the documentation only and runs in the page on a prebuilt index.
- Docs pages render the repository's own Markdown; they do not get separately written copy.

## Content boundary

- Copy states what Pail does: "supports Amazon S3, …", not "has been tested with". No claims of proof, testing or verification on the site, and no claims beyond what `docs/dev/status.md` records.
- Design goals, implementation details, agent instructions and review notes never appear in rendered text, attributes, ARIA, alt text, comments or client-visible data.
- Do not invent claims, metrics, customers, testimonials, integrations or capabilities. Every factual sentence must be backed by README.md, CHANGELOG.md, docs/cli.md, docs/guide/, docs/dev/status.md or tools/release. The feature views show sample data with the shapes Pail really produces (24 parts of 64 MiB for a 1.5 GiB file, presigned query parameters, the lines of `pail sync --dry-run`).

## Terminology and content sources

- Domain terminology: `cmd/pail-desktop/locales/en.json`, `zh-Hans.json`, and the glossary in `docs/dev/design.md`.
- Legal: the trademark notice in README.md.

## Visual and interaction constraints

- Typography: system font stacks only, no web fonts: the system UI face with the platform's CJK face for text, and the system monospace face for code, keys and CLI output. Sizes in rem; headings are tight and large and balanced; hierarchy comes from size, not from extra families.
- Forbidden: stock photography, emoji, uppercase eyebrow labels above headings, numbered section markers, testimonials, logo walls, coloured accent bars on the left edge of items (current, selected or quoted).
- Standards: every page passes html-validate (W3C HTML) with no exceptions; native controls before ARIA roles. Follow MDN guidance for `prefers-reduced-motion`, `prefers-contrast`, `forced-colors` and `theme-color`.
- Accessibility: text contrast 4.5:1 (3:1 for display sizes), visible focus rings, skip link, labelled controls, `lang` on the document.
- Layout of the landing page, in order: the hero in two columns (version link, title, one sentence and two buttons on the left; the point-cloud pail catching object keys, with its upload log, on the right); the app screenshot with its four regions named under it; four features as a list beside one app window that shows the open feature (on narrow screens an accordion with the window inside the open feature); the other features as a filterable list; the command line in a terminal (beside its text on wide screens); privacy as three facts; the download table, one row per system; the footer. The header has the same width on every page. Section titles are one sentence in two tones: the lead in the text colour, the rest in the secondary colour.
- The signature object is the pail of points from the icon: three strata and a handle that assemble on load, turn slowly, lean toward the pointer and catch falling objects. Nothing else is decoration.
- Motion: the pail and its falling keys; the screenshot tilting flat as it scrolls in (where scroll-driven animations exist); the feature tour opening the next feature every nine seconds on wide screens until it is clicked, with a thin progress line, and the open feature's view playing, only while on screen; the terminal types each example; cross-document view transitions keep the header and docs navigation in place. Under reduced motion none of these run. There is no pause button, and nothing is drawn over the screenshot.
- Docs pages are three columns: the navigation, the document (about 740px), and "On this page". Search is only in the header. On narrow screens the navigation folds into one bar under the header that names the current group and page and opens the page list. Current items are marked by weight and colour, plus a background in the navigation, never by a bar. Code blocks are dark in both themes and have a copy button.

## Privacy and multimodal evidence

- Screenshots come only from `--demo` mode; no real account data.
