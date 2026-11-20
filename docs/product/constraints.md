# Pail landing site — product and UX constraints

Scope: the public landing site at https://pail.download (`site/`, built by `tools/site`). The desktop app is not governed by this file; its rules are in `docs/dev/design.md`.

## Product

- Product type: static marketing site for an open-source desktop app and CLI.
- Primary users: people who manage S3-compatible storage and are deciding whether to install Pail.
- Primary jobs: understand what Pail does, download the right archive, read how to install and use it, find the source.
- Pages: the landing page, plus on-site docs rendered from the repository (the README has the sections Features, Install, Quick start, Command line, Data and privacy, Tested platforms and services, Build from source and License, in the Chinese README 功能, 安装, 快速开始, 命令行, 数据与隐私, 已测试的平台与服务, 从源码构建 and 许可证; Get started renders Install, Quick start, Command line, Data and privacy, Tested platforms and services and Build from source; the CLI reference from `docs/cli.md` / `docs/cli.zh-CN.md`; the changelog from `CHANGELOG.md` / `CHANGELOG.zh-CN.md`; the license, in English only because the English text is the binding one). Only the open-source project itself — repository, source files, issues, releases and download assets — links to GitHub.
- UI surface classification: marketing.

## Supported environments

- Current Chrome, Edge, Firefox and Safari; must stay usable without JavaScript (content, links and downloads work; the theme toggle, mobile menu and demos are enhancements).
- Widths from 360px to 1920px with no horizontal page scroll; 16px minimum side gutter.
- Keyboard, pointer and touch. `prefers-reduced-motion` stops every animation.
- English at `/`, Simplified Chinese at `/zh/`. Long Chinese and English strings must not clip.
- No third-party requests at runtime: images are self-hosted and no webfonts are loaded (Google Fonts is unreachable for many visitors in mainland China, and the product promises no telemetry).

## Must preserve

- Colors come from the Paper and Graphite palettes of the app, in `cmd/pail-desktop/theme.go`.
- Light and dark follow the system by default; a manual System / Light / Dark control persists the choice.
- Selected, current and on states use at least two cues, as in `docs/dev/design.md`.
- One solid primary button per view.
- Download links point to GitHub Releases assets named by `tools/release`. The mirror at https://pan.zsh.moe/Pail/v<version>/, with the same file names, is always offered as a second source for the same version.
- No analytics, trackers, cookies or external embeds.

## Explicit non-goals

- No site search or command palette, blog, account, pricing or newsletter.
- Docs pages render the repository's own Markdown; they do not get separately written copy.
- No claims about providers or platforms beyond `docs/dev/status.md` evidence.

## Content boundary

- Design goals, implementation details, agent instructions and review notes never appear in rendered text, attributes, ARIA, alt text, comments or client-visible data.
- Do not invent claims, metrics, customers, testimonials, integrations or capabilities. Every factual sentence must be backed by README.md, CHANGELOG.md, docs/cli.md, docs/dev/status.md or tools/release.

## Terminology and content sources

- Domain terminology: `cmd/pail-desktop/locales/en.json`, `zh-Hans.json`, and the glossary in `docs/dev/design.md`.
- Legal: the trademark notice in README.md.

## Visual and interaction constraints

- Typography: the operating system's default sans-serif (San Francisco, Segoe UI, Roboto, Noto Sans, Ubuntu, Cantarell, then Helvetica/Arial) with CJK fallbacks (PingFang SC, Microsoft YaHei, Noto Sans CJK SC), ending in the generic `sans-serif`; on Chinese pages, headings put the CJK face first so a heading that mixes Latin and Han is one font at one weight; the system monospace (`ui-monospace`, SF Mono, Cascadia Mono, Consolas, …) only for code, keys and CLI output. No webfonts, no serif or italic display accents. Hierarchy comes from size and weight, not from a second family.
- Forbidden: stock photography, hand-drawn SVG illustration, emoji, gradient or blurred-blob backgrounds, grain overlays, glows, cursor spotlights, announcement pills, uppercase eyebrow labels above headings, oversized decorative wordmarks, faux window controls.
- Standards: every page passes html-validate (W3C HTML) with no exceptions; native controls before ARIA roles. Follow MDN guidance for `prefers-reduced-motion`, `prefers-contrast`, `forced-colors` and `theme-color`.
- Accessibility: text contrast 4.5:1 (3:1 for display sizes), visible focus rings, skip link, labelled controls, `lang` on the document.
- Layout: the landing page is one narrow column (1040px at most). Each section starts with its heading and a short text, left-aligned and no wider than 640px, with the thing it describes below at the full column width; the download section is centred. Short items (features, privacy points) are a grid of bordered cells with a small icon, title and sentence, three or two across, one across on phones; the two features with a demo are wider cards with the demo below the text. The hero keeps to a version line, the title, one sentence, two buttons and one line naming the platforms; the tested services are a row of names under the screenshot, with the custom-endpoint note below them. The hero has a faint dot grid that fades out downward; there is no other background decoration.
- Motion: one load sequence in the hero (the version line, title, text, buttons and facts rise 12px one after another, then the screenshot), no entrance animation on scroll, demos only while visible, all disabled under reduced motion. Animated demos never change the size of any box (no layout shift): fixed-size containers, tabular numerals, and state changes by color, background and border only. Docs pages have no entrance animation.

## Privacy and multimodal evidence

- Screenshots come only from `--demo` mode; no real account data.
