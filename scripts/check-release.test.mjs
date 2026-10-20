import assert from 'node:assert/strict';
import { test } from 'node:test';
import { checkRelease, requiredJobs } from './check-release.mjs';

const sha = 'a'.repeat(40);
function fixture(change = () => {}) {
  return path => {
    const workflow = path.includes('codeql') || path.includes('/runs/2') ? 'codeql.yml' : 'ci.yml';
    const run = {
      id: workflow === 'ci.yml' ? 1 : 2, head_sha: sha, head_branch: 'master',
      path: `.github/workflows/${workflow}`, repository: { full_name: 'DejavuMoe/Pail' },
      event: 'push', status: 'completed', conclusion: 'success', run_attempt: 1,
      html_url: `https://github.com/DejavuMoe/Pail/actions/runs/${workflow}`,
    };
    let result;
    if (path.includes('/workflows/')) result = [{ workflow_runs: [run] }];
    else if (path.includes('/jobs?')) result = [{ jobs: requiredJobs[workflow].map(name => ({
      name, head_sha: sha, status: 'completed', conclusion: 'success',
    })) }];
    else result = [run];
    change(result, path);
    return result;
  };
}

test('requires every job for the exact source commit', () => {
  assert.equal(checkRelease('DejavuMoe/Pail', sha, fixture()).length, 2);
});

for (const [field, value] of Object.entries({
  head_sha: 'b'.repeat(40), head_branch: 'main', path: '.github/workflows/other.yml',
  event: 'pull_request', status: 'in_progress', conclusion: 'failure',
  repository: { full_name: 'someone/Pail' },
})) {
  test(`rejects invalid workflow ${field}`, () => {
    assert.throws(() => checkRelease('DejavuMoe/Pail', sha, fixture((result, path) => {
      if (path.includes('/workflows/')) result[0].workflow_runs[0][field] = value;
    })));
  });
}

for (const state of ['missing', 'skipped', 'failure', 'wrong-sha']) {
  test(`rejects ${state} jobs`, () => {
    assert.throws(() => checkRelease('DejavuMoe/Pail', sha, fixture((result, path) => {
      if (!path.includes('/jobs?')) return;
      if (state === 'missing') result[0].jobs.pop();
      else if (state === 'wrong-sha') result[0].jobs[0].head_sha = 'b'.repeat(40);
      else result[0].jobs[0].conclusion = state;
    })));
  });
}

test('fails closed on absent checks, API errors and changed attempts', () => {
  assert.throws(() => checkRelease('DejavuMoe/Pail', sha, () => [{ workflow_runs: [] }]));
  assert.throws(() => checkRelease('DejavuMoe/Pail', sha, () => { throw new Error('API unavailable'); }));
  assert.throws(() => checkRelease('DejavuMoe/Pail', sha, fixture((result, path) => {
    if (/\/runs\/\d+$/.test(path)) result[0].run_attempt++;
  })));
});
