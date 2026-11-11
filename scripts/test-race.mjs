import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const desktop = 'github.com/DejavuMoe/Pail/cmd/pail-desktop';

export function corePackages(output) {
  const packages = output.trim().split(/\s+/);
  assert.ok(packages.includes(desktop), 'desktop package missing from go list');
  const core = packages.filter(name => name !== desktop);
  assert.ok(core.length > 0, 'no remaining race packages');
  return core;
}

export function desktopShards(output) {
  const names = output.split(/\r?\n/).filter(name => /^(Test|Fuzz|Example)\S*$/.test(name));
  assert.equal(new Set(names).size, names.length, 'duplicate test names');
  const shards = [[], []];
  names.forEach((name, i) => shards[i % shards.length].push(name));
  return shards.map(names => {
    assert.ok(names.length > 0, 'empty desktop race shard');
    return `^(${names.join('|')})$`;
  });
}

if (import.meta.main) {
  try {
    const part = process.argv[2];
    assert.ok(['core', 'desktop-1', 'desktop-2'].includes(part), 'expected core, desktop-1 or desktop-2');
    const cwd = fileURLToPath(new URL('..', import.meta.url));
    const capture = args => execFileSync('go', args, { cwd, encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 });
    const args = ['test', '-race', '-count=1', '-timeout', '15m'];
    if (part === 'core') {
      args.push(...corePackages(capture(['list', './...'])));
    } else {
      const shards = desktopShards(capture(['test', '-race', '-list', '^(Test|Fuzz|Example)', desktop]));
      args.push('-run', shards[Number(part.at(-1)) - 1], desktop);
    }
    execFileSync('go', args, { cwd, stdio: 'inherit' });
  } catch (error) {
    console.error(error.message);
    process.exitCode = error.status || 1;
  }
}
