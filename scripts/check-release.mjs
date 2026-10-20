import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';

export const requiredJobs = {
  'ci.yml': [
    'Test windows/amd64', 'Test windows/arm64', 'Test linux/amd64',
    'Test linux/arm64', 'Test darwin/amd64', 'Test darwin/arm64',
    'Quality', 'Race', 'MinIO',
  ],
  'codeql.yml': [
    'Analyze go (linux)', 'Analyze go (windows)', 'Analyze go (darwin)',
    'Analyze javascript-typescript (linux)', 'Analyze actions (linux)',
  ],
};

function github(path) {
  return JSON.parse(execFileSync('gh', ['api', path, '--paginate', '--slurp'], {
    encoding: 'utf8', timeout: 60_000, maxBuffer: 8 * 1024 * 1024,
  }));
}

export function checkRelease(repository, sha, api = github) {
  assert.equal(repository, 'DejavuMoe/Pail');
  assert.match(sha, /^[a-f0-9]{40}$/);
  const verified = [];
  for (const [workflow, names] of Object.entries(requiredJobs)) {
    const runs = api(`repos/${repository}/actions/workflows/${workflow}/runs?branch=master&head_sha=${sha}&per_page=100`)
      .flatMap(page => page.workflow_runs)
      .filter(run => ['push', 'workflow_dispatch'].includes(run.event))
      .sort((a, b) => b.id - a.id);
    const run = runs[0];
    assert.ok(run, `${workflow}: no trusted master run for ${sha}`);
    assert.equal(run.head_sha, sha, `${workflow}: wrong commit`);
    assert.equal(run.head_branch, 'master', `${workflow}: wrong branch`);
    assert.equal(run.path, `.github/workflows/${workflow}`, `${workflow}: wrong workflow`);
    assert.equal(run.repository.full_name, repository, `${workflow}: wrong repository`);
    assert.equal(run.status, 'completed', `${workflow}: still running`);
    assert.equal(run.conclusion, 'success', `${workflow}: latest run did not pass`);
    const jobs = api(`repos/${repository}/actions/runs/${run.id}/jobs?filter=latest&per_page=100`)
      .flatMap(page => page.jobs);
    for (const name of names) {
      assert.equal(jobs.filter(job => job.name === name).length, 1, `${workflow}: missing or duplicate ${name}`);
    }
    for (const job of jobs) {
      assert.equal(job.head_sha, sha, `${job.name}: wrong commit`);
      assert.equal(job.status, 'completed', `${job.name}: still running`);
      assert.equal(job.conclusion, 'success', `${job.name}: did not pass`);
    }
    // A rerun starting during lookup must not reuse an earlier green attempt.
    const [current] = api(`repos/${repository}/actions/runs/${run.id}`);
    assert.equal(current.run_attempt, run.run_attempt, `${workflow}: attempt changed`);
    assert.equal(current.status, 'completed', `${workflow}: run changed`);
    assert.equal(current.conclusion, 'success', `${workflow}: run changed`);
    verified.push(`${workflow}: ${run.html_url} (attempt ${run.run_attempt})`);
  }
  return verified;
}

if (import.meta.main) {
  try {
    console.log(checkRelease(process.argv[2], process.argv[3]).join('\n'));
  } catch (error) {
    console.error(`Release blocked: ${error.message}`);
    process.exitCode = 1;
  }
}
