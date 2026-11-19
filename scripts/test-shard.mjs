import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const desktop = 'github.com/DejavuMoe/Pail/cmd/pail-desktop';
const shardCount = 2;
// Tests at or above this many seconds keep their own weight; faster ones
// share the measured average, so ordinary test additions need no update.
const weightThreshold = 0.2;

export function corePackages(output) {
  const packages = output.trim().split(/\s+/);
  assert.ok(packages.includes(desktop), 'desktop package missing from go list');
  const core = packages.filter(name => name !== desktop);
  assert.ok(core.length > 0, 'no remaining race packages');
  return core;
}

function checkWeights({ defaultSeconds, seconds }) {
  const valid = value => Number.isFinite(value) && value >= 0;
  assert.ok(valid(defaultSeconds), 'invalid defaultSeconds');
  for (const [name, value] of Object.entries(seconds)) assert.ok(valid(value), `invalid weight for ${name}`);
}

// Weights only balance the shards; the discovered inventory decides coverage.
// Heaviest first, each to the lighter shard; ties keep Go's discovered order.
export function desktopShards(output, weights = { defaultSeconds: 1, seconds: {} }) {
  checkWeights(weights);
  const names = output.split(/\r?\n/).filter(name => /^(Test|Fuzz|Example)\S*$/.test(name));
  assert.equal(new Set(names).size, names.length, 'duplicate test names');
  const tests = names.map((name, index) => ({
    name, index, weight: Object.hasOwn(weights.seconds, name) ? weights.seconds[name] : weights.defaultSeconds,
  }));
  const shards = Array.from({ length: shardCount }, () => ({ load: 0, tests: [] }));
  for (const test of [...tests].sort((a, b) => b.weight - a.weight || a.index - b.index)) {
    const shard = shards.reduce((min, shard) => shard.load < min.load ? shard : min);
    shard.load += test.weight;
    shard.tests.push(test);
  }
  return shards.map(({ tests }) => {
    assert.ok(tests.length > 0, 'empty desktop race shard');
    return `^(${tests.sort((a, b) => a.index - b.index).map(test => test.name).join('|')})$`;
  });
}

// Converts `go test -json` output for the desktop package into a weights file.
export function weightsFromTestJSON(text) {
  const elapsed = new Map();
  for (const line of text.split(/\r?\n/)) {
    if (!line.startsWith('{')) continue;
    const event = JSON.parse(line);
    if (event.Package === desktop && event.Test && !event.Test.includes('/') &&
        ['pass', 'fail'].includes(event.Action)) {
      elapsed.set(event.Test, event.Elapsed);
    }
  }
  assert.ok(elapsed.size > 0, `no ${desktop} test results`);
  const light = [...elapsed.values()].filter(value => value < weightThreshold);
  const average = light.length ? light.reduce((sum, value) => sum + value, 0) / light.length : 0;
  const seconds = Object.fromEntries([...elapsed]
    .filter(([, value]) => value >= weightThreshold)
    .sort(([a, x], [b, y]) => y - x || (a < b ? -1 : 1)));
  const weights = { defaultSeconds: Math.round(average * 1000) / 1000, seconds };
  checkWeights(weights);
  return weights;
}

// Flags shared by every shard of a mode; `desktop` is added for the desktop
// shards. Under -race the desktop tests run one at a time: mygo keeps
// package-level state (the z-order painters set by ZIndex, the frame-stats
// switch) that one UI thread writes in the app but parallel Testers write at
// once, which the detector reports although no app code races. `list` builds the same test binary
// that `run` executes, so the inventory and the shard agree.
export const modes = {
  race: { list: ['-race'], run: ['-race', '-shuffle=on', '-count=1', '-timeout', '15m'], desktop: ['-parallel', '1'] },
  native: { list: ['-vet=all'], run: ['-vet=all', '-shuffle=on', '-count=1', '-timeout', '10m'], desktop: [] },
};
export const parts = ['core', ...Array.from({ length: shardCount }, (_, i) => `desktop-${i + 1}`)];

if (import.meta.main) {
  try {
    const [mode, part] = process.argv.slice(2);
    const cwd = fileURLToPath(new URL('..', import.meta.url));
    if (mode === 'weights') {
      assert.ok(part, 'usage: test-shard.mjs weights <go test -json output>');
      console.log(JSON.stringify(weightsFromTestJSON(readFileSync(part, 'utf8')), null, 2));
    } else {
      const usage = `usage: test-shard.mjs <${Object.keys(modes).join('|')}> <${parts.join('|')}>, or weights <file>`;
      assert.ok(Object.hasOwn(modes, mode) && parts.includes(part), usage);
      const capture = args => execFileSync('go', args, { cwd, encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 });
      const args = ['test', ...modes[mode].run];
      if (part === 'core') {
        args.push(...corePackages(capture(['list', './...'])));
      } else {
        const weights = JSON.parse(readFileSync(new URL('test-shard-weights.json', import.meta.url), 'utf8'));
        const inventory = capture(['test', ...modes[mode].list, '-list', '^(Test|Fuzz|Example)', desktop]);
        args.push(...modes[mode].desktop, '-run', desktopShards(inventory, weights)[parts.indexOf(part) - 1], desktop);
      }
      execFileSync('go', args, { cwd, stdio: 'inherit' });
    }
  } catch (error) {
    console.error(error.message);
    process.exitCode = error.status || 1;
  }
}
