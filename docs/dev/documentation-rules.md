# Documentation rules for agents

Read this before you write or edit any Markdown or site copy in this repository. [writing.md](writing.md) covers style (sentences, wording, Chinese typography, Markdown limits). This file decides what goes in at all.

## Audience test

User-facing files are `README*.md`, `docs/cli*.md`, `CHANGELOG*.md` and `site/locales/`. Their readers install, use or upgrade Pail. Keep a sentence only if one of those readers would act on it or notice it. If not, delete it. Developer files (`CONTRIBUTING.md`, `docs/dev/`) are for people changing the code, but the same rule applies: no filler and no history that nobody needs.

## Never put in user-facing files

- Changes to the website, documentation, CI, build, tests, dependencies or refactors. A changelog entry exists only for behaviour a user can see in the desktop app or the command line.
- Why the code works the way it does: thresholds, frame timing, library behaviour, "so it is not confused with...". Write what the user sees or can do.
- Development process: how or when something was verified, who approved it, commit SHAs, run IDs, tool versions, pixel or timing figures.
- Unchanged behaviour, repeated headings, summaries that restate the previous sentence, and marketing adjectives.
- Internal identifiers: package paths, test names, function names, CI product names.

## Changelog entries

- One entry per user-visible change. For a fix, say what went wrong and what happens now.
- Write the English and Chinese versions in the same commit, with the same headings in the same order.
- Add entries under an `[Unreleased]` heading during development, and rename it to the release version when releasing.

## Keep the docs true to the repository state

- Check `git tag` and the dated headings in `CHANGELOG.md` before writing about a version. A tagged version is released: write it in the past tense, and do not write "before the first release" or "until it is published".
- When a version ships, search the repository for the previous version string and update the examples (`README*.md`, `CONTRIBUTING.md`, `docs/dev/ci.md`, `docs/dev/site.md`, `tools/site/main.go` comments). Leave test fixtures, dated changelog sections and historical records such as the security triage note alone.
- Platform and service claims follow [status.md](status.md). Do not claim more than it records.

## Before you finish

- Run `go test ./tools/site/`. It checks the writing rules and that both languages have matching headings.
- Re-read each new sentence against the audience test above.
