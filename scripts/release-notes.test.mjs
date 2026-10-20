import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { releaseNotes, semver } from './release-notes.mjs';

test('release notes require matching bilingual sections', () => {
  const text = '# Changelog\n\n## [0.1.0] - 2026-10-20\n\n### Added\n\n- [Test](docs/status.md)\n\n## [0.0.1] - 2026-01-01\n\nOld\n';
  const notes = releaseNotes('v0.1.0', text, text);
  assert.ok(notes.includes('## 简体中文'));
  assert.ok(!notes.includes('Old'));
  assert.ok(notes.includes('https://github.com/DejavuMoe/Pail/blob/v0.1.0/docs/status.md'));
  assert.throws(() => releaseNotes('v0.2.0', text, text));
  assert.throws(() => releaseNotes('v0.1.0', text, ''));
});

test('versions follow SemVer and local/CI Go versions agree', () => {
  for (const v of ['0.1.0', '1.2.3-rc.1', '1.2.3+build.42']) assert.ok(semver.test(v));
  for (const v of ['v0.1.0', '01.2.3', '1.2.3-01', '../1.2.3']) assert.ok(!semver.test(v));
  const mise = readFileSync(new URL('../mise.toml', import.meta.url), 'utf8');
  const mod = readFileSync(new URL('../go.mod', import.meta.url), 'utf8');
  assert.equal(mise.match(/^go = "([^"]+)"/m)?.[1], mod.match(/^go (.+)$/m)?.[1]);
});
