import assert from 'node:assert/strict';
import { test } from 'node:test';
import { corePackages, desktopShards } from './test-race.mjs';

test('desktop shards cover every test, example and fuzz seed exactly once', () => {
  const names = ['TestA', 'TestAAgain', 'TestB', 'FuzzNames', 'ExampleApp', 'Test中文'];
  const output = [...names, 'ok\tgithub.com/DejavuMoe/Pail/cmd/pail-desktop\t0.01s', ''].join('\r\n');
  const selectors = desktopShards(output).map(pattern => new RegExp(pattern));
  for (const name of names) {
    assert.equal(selectors.filter(re => re.test(name)).length, 1, name);
  }
  for (const re of selectors) assert.equal(re.test('TestAExtra'), false);
  assert.deepEqual(selectors.map(re => names.filter(name => re.test(name)).length), [3, 3]);
});

test('empty, missing or duplicate test inventories fail closed', () => {
  for (const output of ['', 'ok\tpackage', 'TestA', 'TestA\nTestA']) {
    assert.throws(() => desktopShards(output));
  }
});

test('core keeps every package except the desktop package itself', () => {
  const desktop = 'github.com/DejavuMoe/Pail/cmd/pail-desktop';
  const others = [desktop + '/icons', 'github.com/DejavuMoe/Pail/internal/app', 'github.com/DejavuMoe/Pail/tools/site'];
  assert.deepEqual(corePackages([desktop, ...others].join('\n') + '\n'), others);
  for (const output of ['', others.join('\n'), desktop]) {
    assert.throws(() => corePackages(output));
  }
});
