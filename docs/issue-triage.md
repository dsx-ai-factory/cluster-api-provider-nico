# Issue Triage

Triage records whether an issue is actionable and how urgently it needs a fix.
Engineering maintainers record acceptance by assigning a priority so you can
track the decision and the fix deadline.

## Labels and Fix SLAs

The following labels distinguish engineering priority from the impact you report.

| Label | Meaning | Fix SLA |
|---|---|---|
| `needs-triage` | Engineering has not assigned a priority yet. | Assign a priority within seven calendar days of opening or reopening. |
| `priority/P0` | Major functionality does not work, significantly affects users or tenants, and has no workaround. | Publish a fix within 30 calendar days of acceptance. |
| `priority/P1` | The provider remains usable but does not behave as documented, with small user or tenant impact and an available workaround. | Publish a fix within six calendar months of acceptance. |
| `priority/P2` | Minor defects or minor effects on users, tenants, or performance. | No fix commitment. |
| `needs-info` | The reporter needs to provide more information. | No separate fix SLA. An existing priority and deadline still apply. |
| `lifecycle/stale` | An issue has had no activity for 90 days. | Close after another 30 days without activity. |
| `lifecycle/frozen` | An issue is exempt from inactivity handling. | An existing priority and deadline still apply. |

These service-level agreements (SLAs) start at acceptance. Acceptance is the
first engineering priority assignment, recorded in the issue's label history.
It completes triage and starts the fix clock. A fix is available when a release
containing it is published; merging a pull request alone does not meet the SLA.
Use non-closing issue references in implementation pull requests so the issue
can remain open until that release is available.

Engineering records the acceptance date, target date, and planned release in an
issue comment for P0 and P1. Count six calendar months from acceptance for P1.
If the target month lacks the acceptance day, use that month's last day.

Set exactly one priority label. Explain priority changes and record the revised
target date using the original acceptance date. Removing and reapplying a label
or reopening the same unresolved issue does not restart the fix clock. A
reopened issue needs a priority review within seven calendar days, even when
its existing priority means it has no `needs-triage` label.

If a deadline is at risk, Engineering explains the delay and next steps in the
issue before the target date. A delay explanation does not extend the SLA.
Automation manages labels and inactivity; maintainers track fix deadlines and
release completion.

Setting a priority removes `needs-triage`. Removing the last priority restores
it. `needs-info`, assignment, and kind labels do not complete triage. When more
information is needed, explain what blocks the decision and follow up during
the weekly review.

Suspected vulnerabilities and known vulnerability reports follow the
[security policy](../SECURITY.md), separately from ordinary issue triage.
Do not include vulnerability details in public issues.

## Ownership and Permissions

Engineering reviews bugs weekly. Product reviews feature requests weekly and
recommends their scope and urgency to Engineering. Engineering assigns and
changes priorities for all issue kinds. Maintainers review documentation
requests and coordinate their priority with Engineering.

Only designated engineering maintainers with repository Write access or above
apply or modify issue labels and priorities. Product reviewers and contributors
without that authorization record recommendations in comments. Repository
administrators restrict elevated access and regularly audit direct grants,
team grants, and inherited permissions. Automated label maintenance uses the
workflow's limited repository token.

## Maintainer Checklist

Engineering uses the following checklist when recording a triage decision.

1. Confirm that the issue is in scope. Close duplicates with a link to the
   original, and explain why an out-of-scope request is closed.
2. Check the kind label: `bug`, `enhancement`, or `documentation`.
3. Read the impact, reproduction details, affected versions, and workarounds.
   Request missing details with `needs-info` and a comment. Remove the label
   after receiving enough information.
4. Accept actionable work by setting one priority. Apply the definitions above;
   when uncertain between P0 and P1, use P1 and explain the decision. Feature
   and documentation requests also receive a priority.
5. For P0 or P1, record the acceptance date, target date, and planned release.
   Assign an owner, or explain how Engineering will find one. For other work,
   consider `help wanted` or `good first issue`.
6. Before closing an issue as fixed, link the published release containing the
   fix. Distinguish fixed issues from duplicates, declined requests, and issues
   closed due to inactivity.

Review new issues, unanswered information requests, upcoming deadlines, overdue
fixes, and reopened issues at least weekly. Use these queues to find work.

- [Awaiting triage](https://github.com/dsx-ai-factory/cluster-api-provider-nico/issues?q=is%3Aopen+is%3Aissue+label%3Aneeds-triage).
- [Waiting for information](https://github.com/dsx-ai-factory/cluster-api-provider-nico/issues?q=is%3Aopen+is%3Aissue+label%3Aneeds-info).
- [P0 issues](https://github.com/dsx-ai-factory/cluster-api-provider-nico/issues?q=is%3Aopen+is%3Aissue+label%3Apriority%2FP0).
- [P1 issues](https://github.com/dsx-ai-factory/cluster-api-provider-nico/issues?q=is%3Aopen+is%3Aissue+label%3Apriority%2FP1).
- [Recently reopened issues](https://github.com/dsx-ai-factory/cluster-api-provider-nico/issues?q=is%3Aopen+is%3Aissue+sort%3Aupdated-desc).

## Pull Request Responses

Maintainers provide a first review response within 96 hours after a pull request
is ready for review. Draft pull requests are excluded until marked ready. A
response acknowledges the contribution and provides review feedback or a next
step. CI results alone do not provide that feedback.

The weekly maintainer review includes unanswered pull requests, response times,
time to merge or close, and external contributions closed without merging.
Record the reason when closing a contribution without merging and explain
whether a revised contribution would be welcome.

## For Reporters

Use the bug, feature, or documentation form. For usage questions, use
[Discussions](https://github.com/dsx-ai-factory/cluster-api-provider-nico/discussions/categories/q-a).
Report security vulnerabilities through the security policy.

You do not need to act on `needs-triage`. To request a higher priority, comment
with the impact, affected clusters or machines, and available workarounds. The
form's impact field informs the decision; it does not set the priority label.

## Automation

The [Issue Triage Check workflow](../.github/workflows/issue-triage-check.yml)
runs on issue opening, reopening, and relevant label changes. It also reconciles
all open issues hourly and supports a manual run from the Actions tab. It
creates missing triage and lifecycle labels without overwriting existing definitions. It
does not assign priorities, track acceptance dates, or calculate fix deadlines.
Closed issues and pull requests are excluded from triage reconciliation.

The [Stale Issues workflow](../.github/workflows/stale.yml) runs daily and
supports manual runs. It marks issues stale after 90 days without activity and
closes them after another 30 days. Activity removes the stale label. Applying
`lifecycle/frozen` exempts an issue. Features and documentation requests follow
the same inactivity rules as bugs. Pull requests are excluded from automatic
stale handling.

Inactivity closure does not establish that an issue is fixed or satisfy a fix
deadline. Engineering reviews accepted work before it becomes stale and uses
`lifecycle/frozen` for work that must remain open without regular activity.
Scheduled runs can be delayed, so label and closure changes can occur later
than the stated intervals.

Both workflows use hosted runners and the repository's `GITHUB_TOKEN`. Triage
needs `issues: write`. Stale handling also needs `actions: write` to store its
processing state. Neither workflow executes issue content or needs a personal
access token.

## Maintainer Setup

Before enabling this process, repository administrators and maintainers complete
the following checks.

- Confirm Engineering and Product review ownership and restrict elevated access
  to the designated engineering team. Audit access regularly.
- Verify the issue forms against the approved organization templates while
  preserving provider-specific context and secret-redaction guidance.
- Confirm repository coverage in the organization's issue and contribution
  reporting, including first-response event collection and weekly review.
- Record acceptance dates and deadlines for already accepted P0 and P1 issues
  using their original priority history. Review inactive issues before the first
  scheduled stale run and freeze any that must stay open.
- Run Issue Triage Check manually to create the missing triage and lifecycle
  labels and reconcile existing issues. Confirm `lifecycle/frozen` is available
  before the first stale run. The workflows also run automatically after merging.

Run the mocked API and configuration tests locally with this command.

```bash
node --test .github/tests/*.test.cjs
```
