// SPDX-License-Identifier: Apache-2.0
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { resolve } = require('node:path');
const { test } = require('node:test');

const read = (path) => readFileSync(resolve(__dirname, '..', path), 'utf8');
const stale = read('workflows/stale.yml');
const chooser = read('ISSUE_TEMPLATE/config.yml');

// actionlint validates workflow structure; these checks guard destructive policy
// settings against an accidental change or a return to the action's defaults.
function scalar(source, key) {
  const matches = [...source.matchAll(new RegExp(`^ +${key}: ([^\\n]+)$`, 'gm'))];
  assert.equal(matches.length, 1, `Expected exactly one explicit ${key} setting`);
  return matches[0][1].trim().replace(/^['"]|['"]$/g, '');
}

test('inactive issues receive a 90-day warning and a further 30-day grace period', () => {
  assert.equal(scalar(stale, 'days-before-issue-stale'), '90');
  assert.equal(scalar(stale, 'days-before-issue-close'), '30');
  assert.equal(scalar(stale, 'stale-issue-label'), 'lifecycle/stale');
});

test('both automatic stale operations are explicitly disabled for pull requests', () => {
  assert.equal(scalar(stale, 'days-before-pr-stale'), '-1');
  assert.equal(scalar(stale, 'days-before-pr-close'), '-1');
  assert.doesNotMatch(stale, /pull-requests: write/);
});

test('frozen issues are exempt and activity clears stale status', () => {
  assert.equal(scalar(stale, 'exempt-issue-labels'), 'lifecycle/frozen');
  assert.equal(scalar(stale, 'remove-issue-stale-when-updated'), 'true');
});

test('stale action is immutable and issue processing is serialized', () => {
  assert.match(stale, /uses: actions\/stale@[a-f0-9]{40}\b/);
  assert.equal(scalar(stale, 'group'), 'Stale Issues');
  assert.equal(scalar(stale, 'cancel-in-progress'), 'false');
  assert.match(stale, /if: github\.repository == 'dsx-ai-factory\/cluster-api-provider-nico'/);
});

test('inactivity closure is not represented as a released fix', () => {
  assert.match(stale, /Closure due to inactivity does not mean that a fix has been released/);
});

test('issue chooser requires forms and exposes only the security contact', () => {
  assert.match(chooser, /^blank_issues_enabled: false$/m);
  const contacts = [...chooser.matchAll(/^  - name: (.+)$/gm)];
  assert.deepEqual(contacts.map((match) => match[1]), ['Report a security vulnerability']);
  assert.match(chooser, /Do not file public issues for security reports/);
});

test('all public issue forms record kind and triage status', () => {
  for (const [filename, kind] of [
    ['bug_report_form.yml', 'bug'],
    ['feature_request_form.yml', 'enhancement'],
    ['documentation_request.yml', 'documentation'],
  ]) {
    assert.match(read(`ISSUE_TEMPLATE/${filename}`), new RegExp(`^labels: \\["${kind}", "needs-triage"\\]$`, 'm'));
  }
});

test('bug reporting requires private reporting of security vulnerabilities', () => {
  const form = read('ISSUE_TEMPLATE/bug_report_form.yml');
  assert.match(form, /id: security[\s\S]*?not a security vulnerability[\s\S]*?SECURITY\.md[\s\S]*?required: true/);
});

test('policy labels describe acceptance and calendar months without an obsolete day approximation', () => {
  const workflow = read('workflows/issue-triage-check.yml');
  assert.match(workflow, /name: 'priority\/P0'.*30 days of acceptance/);
  assert.match(workflow, /name: 'priority\/P1'.*six calendar months of acceptance/);
  assert.match(workflow, /name: 'lifecycle\/frozen'/);
  assert.doesNotMatch(workflow, /183/);
});
