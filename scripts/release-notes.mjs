import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

export const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/;

export function releaseNotes(tag, en, zh) {
  assert.ok(tag.startsWith('v') && semver.test(tag.slice(1)), 'Expected a v-prefixed semantic version tag');
  return [en, zh].map((text, i) => {
    const section = text.split(/^## /m).find(part => part.startsWith(`[${tag.slice(1)}] - `));
    assert.ok(section?.trim(), `${i ? 'Chinese' : 'English'} changelog lacks ${tag}`);
    const body = section.slice(section.indexOf('\n') + 1).trim()
      .replace(/\]\((?![a-z]+:|#)([^)\s]+)\)/gi,
        `](https://github.com/DejavuMoe/Pail/blob/${tag}/$1)`);
    return `## ${i ? '简体中文' : 'English'}\n\n${body}`;
  }).join('\n\n');
}

if (import.meta.main) {
  console.log(releaseNotes(process.argv[2],
    readFileSync(new URL('../CHANGELOG.md', import.meta.url), 'utf8'),
    readFileSync(new URL('../CHANGELOG.zh-CN.md', import.meta.url), 'utf8')));
}
