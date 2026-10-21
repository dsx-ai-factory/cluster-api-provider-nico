// SPDX-License-Identifier: Apache-2.0
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { resolve } = require('node:path');
const { test } = require('node:test');

// Exercise the exact script Actions runs without a YAML dependency or API access.
const workflow = readFileSync(resolve(__dirname, '../workflows/issue-triage-check.yml'), 'utf8');
const script = workflow.split('          script: |\n')[1]
  .split('\n').map((line) => line.replace(/^ {12}/, '')).join('\n');
const run = new (Object.getPrototypeOf(async function () {}).constructor)('github', 'context', 'core', script);

function issue(labels = [], fields = {}) {
  return { number: 1, state: 'open', labels: labels.map((name) => ({ name })), ...fields };
}

async function reconcile({ live = issue(), payload = { issue: { number: 1 } }, listed = [], missing = [], failures = {} } = {}) {
  const calls = [];
  const labels = new Set(['needs-triage', 'needs-info', 'priority/P0', 'priority/P1', 'priority/P2', 'lifecycle/stale', 'lifecycle/frozen'].filter((name) => !missing.includes(name)));
  const api = {};
  for (const method of ['getLabel', 'createLabel', 'get', 'listForRepo', 'addLabels', 'removeLabel']) {
    api[method] = async (args) => {
      calls.push({ method, ...args });
      if (failures[method]) return failures[method](args, labels);
      if (method === 'getLabel' && !labels.has(args.name)) throw { status: 404 };
      if (method === 'createLabel') labels.add(args.name);
      return { data: method === 'get' ? live : {} };
    };
  }
  await run({ rest: { issues: api }, paginate: async (method, args) => {
    assert.equal(method, api.listForRepo);
    assert.equal(args.state, 'open');
    assert.equal(args.per_page, 100);
    calls.push({ method: 'paginate', ...args });
    return listed;
  } }, { repo: { owner: 'example', repo: 'provider' }, payload }, { info() {} });
  return calls;
}

const mutations = (calls) => calls.filter(({ method }) => ['addLabels', 'removeLabel'].includes(method));

test('unprioritized issues receive needs-triage without replacing other labels', async () => {
  const calls = mutations(await reconcile({ live: issue(['bug', 'needs-info']) }));
  assert.deepEqual(calls, [{ method: 'addLabels', owner: 'example', repo: 'provider', issue_number: 1, labels: ['needs-triage'] }]);
});

for (const priority of ['priority/P0', 'priority/P1', 'priority/P2']) {
  test(`${priority} clears needs-triage and preserves other labels`, async () => {
    assert.deepEqual(mutations(await reconcile({ live: issue(['needs-triage', priority, 'needs-info']) })), [
      { method: 'removeLabel', owner: 'example', repo: 'provider', issue_number: 1, name: 'needs-triage' },
    ]);
  });
}

test('live state overrides the event snapshot after the last priority is removed', async () => {
  assert.equal(mutations(await reconcile({ payload: { issue: issue(['priority/P0']), action: 'unlabeled' }, live: issue() }))[0].method, 'addLabels');
});

test('reopened issues are triaged according to their current priority', async () => {
  assert.equal(mutations(await reconcile({ payload: { issue: { number: 1 }, action: 'reopened' } }))[0].method, 'addLabels');
});

test('already consistent issues need no label mutation', async () => {
  for (const labels of [['needs-triage'], ['priority/P1'], ['priority/P0', 'priority/P1']]) {
    assert.deepEqual(mutations(await reconcile({ live: issue(labels) })), []);
  }
});

test('closed issues and pull requests are ignored even in event mode', async () => {
  for (const live of [issue([], { state: 'closed' }), issue([], { pull_request: {} })]) {
    assert.deepEqual(mutations(await reconcile({ live })), []);
  }
});

test('scheduled and manual sweeps paginate issues and exclude pull requests', async () => {
  for (const payload of [{}, { inputs: {} }]) {
    const calls = await reconcile({ payload, listed: [issue([], { number: 10 }), issue([], { number: 11, pull_request: {} }), issue(['priority/P1', 'needs-triage'], { number: 12 }), issue([], { state: 'closed' })] });
    assert.deepEqual(mutations(calls).map(({ issue_number, method }) => [issue_number, method]), [[10, 'addLabels'], [12, 'removeLabel']]);
    assert.equal(calls.some(({ method }) => method === 'get'), false);
  }
});

test('missing labels are created with colors and descriptions', async () => {
  const calls = await reconcile({ missing: ['needs-triage', 'priority/P0'] });
  const created = calls.filter(({ method }) => method === 'createLabel');
  assert.deepEqual(created.map(({ name }) => name), ['needs-triage', 'priority/P0']);
  assert.ok(created.every(({ color, description }) => /^[0-9a-f]{6}$/.test(color) && description));
});

test('existing label definitions are left unchanged', async () => {
  assert.equal((await reconcile()).some(({ method }) => method === 'createLabel'), false);
});

test('concurrent label creation is tolerated after verifying the label exists', async () => {
  const calls = await reconcile({ missing: ['needs-triage'], failures: { createLabel: ({ name }, labels) => {
    labels.add(name);
    throw { status: 422 };
  } } });
  assert.equal(calls.filter(({ method, name }) => method === 'getLabel' && name === 'needs-triage').length, 2);
});

test('validation errors without a concurrently created label fail', async () => {
  await assert.rejects(reconcile({ missing: ['needs-triage'], failures: { createLabel: async () => { throw { status: 422 }; } } }), { status: 404 });
});

test('a concurrently removed needs-triage label is harmless', async () => {
  await reconcile({ live: issue(['priority/P1', 'needs-triage']), failures: { removeLabel: async () => { throw { status: 404 }; } } });
});

for (const method of ['getLabel', 'createLabel', 'get', 'addLabels', 'removeLabel']) {
  test(`${method} failures propagate instead of reporting success`, async () => {
    await assert.rejects(reconcile({ live: issue(method === 'removeLabel' ? ['priority/P1', 'needs-triage'] : []), missing: method === 'createLabel' ? ['needs-triage'] : [], failures: { [method]: async () => { throw { status: 403 }; } } }), { status: 403 });
  });
}

test('string labels returned by the API are also supported', async () => {
  assert.equal(mutations(await reconcile({ live: issue([], { labels: ['priority/P1', 'needs-triage'] }) }))[0].method, 'removeLabel');
});
