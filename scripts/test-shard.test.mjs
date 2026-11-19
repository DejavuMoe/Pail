import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { corePackages, desktopShards, modes, parts, weightsFromTestJSON } from './test-shard.mjs';

const desktop = 'github.com/DejavuMoe/Pail/cmd/pail-desktop';

function coverage(output, weights) {
  return desktopShards(output, weights).map(pattern => new RegExp(pattern));
}

test('desktop shards cover every test, example and fuzz seed exactly once', () => {
  const names = ['TestA', 'TestAAgain', 'TestB', 'FuzzNames', 'ExampleApp', 'Test中文'];
  const output = [...names, `ok\t${desktop}\t0.01s`, ''].join('\r\n');
  const selectors = coverage(output);
  for (const name of names) {
    assert.equal(selectors.filter(re => re.test(name)).length, 1, name);
  }
  for (const re of selectors) assert.equal(re.test('TestAExtra'), false);
  assert.deepEqual(selectors.map(re => names.filter(name => re.test(name)).length), [3, 3]);
});

test('weights balance the shards without changing coverage', () => {
  const names = ['TestSlow', 'TestA', 'TestB', 'TestMedium', 'TestNew', 'TestC'];
  const weights = { defaultSeconds: 1, seconds: { TestSlow: 10, TestMedium: 6, TestA: 2, TestGone: 50 } };
  const selectors = coverage(names.join('\n'), weights);
  const load = re => names.filter(name => re.test(name))
    .reduce((sum, name) => sum + (weights.seconds[name] ?? weights.defaultSeconds), 0);
  for (const name of names) assert.equal(selectors.filter(re => re.test(name)).length, 1, name);
  assert.deepEqual(selectors.map(load), [11, 10]);
  assert.ok(selectors[0].test('TestSlow') && selectors[1].test('TestMedium'));
});

test('checked-in weights are valid and name existing kinds of tests', () => {
  const weights = JSON.parse(readFileSync(new URL('test-shard-weights.json', import.meta.url), 'utf8'));
  assert.ok(Object.keys(weights.seconds).length > 0);
  for (const name of Object.keys(weights.seconds)) assert.match(name, /^(Test|Fuzz|Example)\S*$/);
  assert.equal(desktopShards(Object.keys(weights.seconds).join('\n'), weights).length, 2);
});

test('empty, missing or duplicate test inventories and bad weights fail closed', () => {
  for (const output of ['', 'ok\tpackage', 'TestA', 'TestA\nTestA']) {
    assert.throws(() => desktopShards(output));
  }
  for (const weights of [
    { defaultSeconds: Number.NaN, seconds: {} },
    { defaultSeconds: 1, seconds: { TestA: -1 } },
    { defaultSeconds: 1, seconds: { TestA: '2' } },
  ]) {
    assert.throws(() => desktopShards('TestA\nTestB', weights));
  }
});

test('weights come from top-level desktop results in go test -json output', () => {
  const event = (Test, Action, Elapsed, Package = desktop) => JSON.stringify({ Package, Test, Action, Elapsed });
  const output = [
    event('TestSlow', 'run'), event('TestSlow/sub', 'pass', 9), event('TestSlow', 'pass', 3),
    event('TestFast', 'pass', 0.1), event('TestFaster', 'pass', 0.05), event('TestFailing', 'fail', 0.5),
    event('TestSkipped', 'skip', 0), event('TestOther', 'pass', 99, 'example.com/other'), 'not json', '',
  ].join('\n');
  assert.deepEqual(weightsFromTestJSON(output), {
    defaultSeconds: 0.075, seconds: { TestSlow: 3, TestFailing: 0.5 },
  });
  assert.throws(() => weightsFromTestJSON(event('TestOther', 'pass', 1, 'example.com/other')));
});

test('core keeps every package except the desktop package itself', () => {
  const others = [desktop + '/icons', 'github.com/DejavuMoe/Pail/internal/app', 'github.com/DejavuMoe/Pail/tools/site'];
  assert.deepEqual(corePackages([desktop, ...others].join('\n') + '\n'), others);
  for (const output of ['', others.join('\n'), desktop]) {
    assert.throws(() => corePackages(output));
  }
});

test('the workflow runs the same flags and parts as the shard script', () => {
  const ci = readFileSync(new URL('../.github/workflows/ci.yml', import.meta.url), 'utf8').replace(/\r\n/g, '\n');
  // A target without parts runs every package in one job with the same flags.
  assert.ok(ci.includes(`go test ${modes.native.run.join(' ')} ./...\n`), 'native flags differ from the workflow');
  assert.ok(ci.includes('node scripts/test-shard.mjs native "$TEST_PART"'));
  assert.ok(ci.includes('node scripts/test-shard.mjs race "$RACE_PART"'));
  assert.deepEqual(ci.match(/^ {8}part: \[([^\]]+)\]$/m)[1].split(/,\s*/), parts);
  const native = new Map();
  for (const [, target, part] of ci.matchAll(/^ {10}- \{target: ([^,}]+), runner: [^,}]+, part: ([^,}]+)\}$/gm)) {
    native.set(target, [...(native.get(target) ?? []), part]);
  }
  assert.ok(native.size > 0, 'no sharded native targets');
  for (const [target, list] of native) assert.deepEqual(list, parts, target);
});

test('race and native shards differ only in their test flags', () => {
  assert.deepEqual(modes.race.run.filter(flag => flag !== '-race'), ['-shuffle=on', '-count=1', '-timeout', '15m']);
  assert.ok(modes.race.list.includes('-race'));
  // Parallel Testers trip the detector on mygo's package-level state.
  assert.deepEqual(modes.race.desktop, ['-parallel', '1']);
  assert.deepEqual(modes.native.desktop, []);
  assert.ok(modes.native.list.includes('-vet=all') && modes.native.run.includes('-vet=all'));
  assert.deepEqual(parts, ['core', 'desktop-1', 'desktop-2']);
});
