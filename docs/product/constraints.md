# Pail landing site — product and UX constraints

Scope: the public landing site at https://pail.download (`designs/pail-site/` prototype, `site/` production). The desktop app is not governed by this file; its rules are in `docs/design.md`.

## Product

- Product type: static marketing site for an open-source desktop app and CLI.
- Primary users: people who manage S3-compatible storage and are deciding whether to install Pail.
- Primary jobs: understand what Pail does, download the right archive, read how to install and use it, find the source.
- Pages: the landing page, plus on-site docs rendered from the repository (Get started from the README sections Install, Quick start, Command line, Build from source, Data and privacy and Platform status; the CLI reference from `docs/cli.md` / `docs/cli.zh-CN.md`; the changelog from `CHANGELOG.md` / `CHANGELOG.zh-CN.md`; the license, in English only because the English text is the binding one). Only the open-source project itself — repository, source files, issues, releases and download assets — links to GitHub.
- UI surface classification: marketing.
- Content profile: `marketing-approved`.

## Supported environments

- Current Chrome, Edge, Firefox and Safari; must stay usable without JavaScript (content, links and downloads work; the theme toggle, mobile menu and demos are enhancements).
- Widths from 360px to 1920px with no horizontal page scroll; 16px minimum side gutter.
- Keyboard, pointer and touch. `prefers-reduced-motion` stops every animation.
- English at `/`, Simplified Chinese at `/zh/`. Long Chinese and English strings must not clip.
- No third-party requests at runtime: images are self-hosted and no webfonts are loaded (Google Fonts is unreachable for many visitors in mainland China, and the product promises no telemetry).

## Must preserve

- Colors come from the Graphite & Paper tokens in `cmd/pail-desktop/theme.go`.
- Light and dark follow the system by default; a manual System / Light / Dark control persists the choice.
- Selected, current and on states use at least two cues, as in `docs/design.md`.
- One solid primary button per view.
- Download links point to GitHub Releases assets named by `scripts/release.ps1`.
- No analytics, trackers, cookies or external embeds.

## Explicit non-goals

- No site search or command palette, blog, account, pricing or newsletter.
- Docs pages render the repository's own Markdown; they do not get separately written copy.
- No claims about providers or platforms beyond `docs/status.md` evidence.

## Non-negotiable content boundary

- The task brief is design input, never product copy.
- Design goals, audience descriptions, implementation details, architecture, aesthetic rationale, agent instructions and internal review notes never appear in rendered text, attributes, ARIA, alt text, comments or client-visible data.
- Marketing copy is allowed only after the user approves it; until then the inventory marks it `review`.
- Do not invent claims, metrics, customers, testimonials, integrations or capabilities. Every factual sentence cites README.md, CHANGELOG.md, docs/cli.md, docs/design.md or scripts/release.ps1 in `content-inventory.json`.

## Approved terminology and content sources

- Domain terminology: `cmd/pail-desktop/locales/en.json`, `zh-Hans.json`, and the glossary in `docs/design.md`.
- Approved copy: none yet; approval of `designs/pail-site/index.html` approves its i18n strings.
- Legal: the trademark notice in README.md.

## Visual and interaction constraints

- Typography: the operating system's default sans-serif (San Francisco, Segoe UI, Roboto, Noto Sans, Ubuntu, Cantarell, then Helvetica/Arial) with CJK fallbacks (PingFang SC, Microsoft YaHei, Noto Sans CJK SC), ending in the generic `sans-serif`; the system monospace (`ui-monospace`, SF Mono, Cascadia Mono, Consolas, …) only for code, keys and CLI output. No webfonts, no serif or italic display accents. Hierarchy comes from size and weight, not from a second family.
- Forbidden: stock photography, hand-drawn SVG illustration, emoji, gradient or blurred-blob backgrounds, grain overlays, glows, cursor spotlights, announcement pills, uppercase eyebrow labels above headings, oversized decorative wordmarks, faux window controls.
- Standards: every page passes html-validate (W3C HTML) with no exceptions; native controls before ARIA roles. Follow MDN guidance for `prefers-reduced-motion`, `prefers-contrast`, `forced-colors` and `theme-color`.
- Accessibility: text contrast 4.5:1 (3:1 for display sizes), visible focus rings, skip link, labelled controls, `lang` on the document.
- Motion: one load sequence, scroll-linked hero tilt, demos only while visible, all disabled under reduced motion. The falling object keys are hidden below 680px. Animated demos never change the size of any box (no layout shift): fixed-size containers, tabular numerals, and state changes by color, background and border only. Docs pages have no entrance animation.

## Privacy and multimodal evidence

- Screenshots come only from `--demo` mode; no real account data.

## Decided

- Deploy: Woodpecker builds the site and rsyncs it over SSH into a static site directory on a designated server (see `docs/site.md`).
