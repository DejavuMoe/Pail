# Pail landing site — product and UX constraints

Scope: the public landing site at https://pail.download (`designs/pail-site/` prototype, `site/` production). The desktop app is not governed by this file; its rules are in `docs/design.md`.

## Product

- Product type: static marketing site for an open-source desktop app and CLI.
- Primary users: people who manage S3-compatible storage and are deciding whether to install Pail.
- Primary jobs: understand what Pail does, download the right archive, find the source, docs and changelog.
- UI surface classification: marketing.
- Content profile: `marketing-approved`.

## Supported environments

- Current Chrome, Edge, Firefox and Safari; must stay usable without JavaScript (content, links and downloads work; the palette, theme toggle and demos are enhancements).
- Widths from 360px to 1920px with no horizontal page scroll; 16px minimum side gutter.
- Keyboard, pointer and touch. `prefers-reduced-motion` stops every animation.
- English at `/`, Simplified Chinese at `/zh/`. Long Chinese and English strings must not clip.
- No third-party requests at runtime: fonts and images are self-hosted (Google Fonts is unreachable for many visitors in mainland China, and the product promises no telemetry).

## Must preserve

- Colors come from the Graphite & Paper tokens in `cmd/pail-desktop/theme.go`.
- Light and dark follow the system by default; a manual System / Light / Dark control persists the choice.
- Selected, current and on states use at least two cues, as in `docs/design.md`.
- One solid primary button per view.
- Download links point to GitHub Releases assets named by `scripts/release.ps1`.
- No analytics, trackers, cookies or external embeds.

## Explicit non-goals

- No documentation site, blog, account, pricing or newsletter.
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

- Typography: display Bricolage Grotesque, accent Instrument Serif italic (Latin only), body Instrument Sans, mono Geist Mono (all SIL OFL); CJK uses system fonts.
- Forbidden: stock photography, hand-drawn SVG illustration, emoji, gradient-heavy backgrounds.
- Accessibility: text contrast 4.5:1 (3:1 for display sizes), visible focus rings, skip link, labelled controls, `lang` on the document.
- Motion: one load sequence, scroll-linked hero tilt, demos only while visible, all disabled under reduced motion.

## Privacy and multimodal evidence

- Screenshots come only from `--demo` mode; no real account data.
- Fonts must be OFL or similar; record licenses in `site/`.

## Open decisions

- Deploy target for `.woodpecker/site.yml` (see `docs/site.md`).
