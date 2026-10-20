# Prototype-first UI

[中文](README.zh-CN.md)

An Agent Skill for designing and implementing UI changes through reviewed
prototypes. It works with [Baoyu-Design](https://github.com/JimLiu/baoyu-design)
to connect product audits, interactive prototypes, approval, and implementation.

Use it for new screens, redesigns, interaction changes, and keeping prototypes
aligned with production across web, desktop, mobile, and browser extensions.

## Requirements

- A file-capable AI agent with shell access.
- Git, Python 3.9+, and Node.js.
- Baoyu-Design for prototype generation.
- A browser or preview tool for visual and interaction review.

## Installation

Run from your project directory and select your agent in the installer:

```sh
npx skills add DejavuMoe/prototype-first-ui --skill prototype-first-ui --copy
npx skills add JimLiu/baoyu-design --skill baoyu-design --copy
```

Alternatively, extract the skill from
[GitHub Releases](https://github.com/DejavuMoe/prototype-first-ui/releases)
into your agent's skills directory.

## Usage

```text
$prototype-first-ui prototype

Design a bulk rename flow for this file manager. Include a preview of the changes,
filename conflicts, and cancellation. Present the prototype for review.
```

After review:

```text
$prototype-first-ui implement

Implement the approved designs/file-manager/index.html from design commit <SHA>.
Limit the change to the bulk rename flow.
```

Agents without named skill invocation can read the installed `SKILL.md` directly.
More examples: [English](references/prompts.en.md) · [中文](references/prompts.zh-CN.md).

## Workflow

Audit → prototype → review → approve → implement → verify.

| Mode | Purpose |
|---|---|
| `audit` | Record current product capabilities, interfaces, and states |
| `explore` | Compare alternative design directions |
| `prototype` | Create or revise an interactive prototype |
| `approve` | Record approval of a specific prototype version |
| `implement` | Implement an approved feature |
| `sync` | Reconcile prototype and production differences |
| `verify` | Check content, visuals, interactions, and accessibility |
| `bootstrap` | Optional repository cleanup and local history reset with a recovery snapshot |

[Skill instructions](SKILL.md) · [Workflow reference](references/workflow.md) ·
[Release guide](RELEASING.md) · [Changelog](CHANGELOG.md)

## License

[MIT](LICENSE)
