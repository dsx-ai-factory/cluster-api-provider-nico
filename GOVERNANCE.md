# Project Governance

This document describes how cluster-api-provider-nico is governed: the roles
people hold, how decisions are made, and how maintainers join and leave. For the
current roster, see [MAINTAINERS.md](MAINTAINERS.md).

## Roles

### Contributors

Anyone who opens an issue, a pull request, or a discussion. Contributors follow
the [Code of Conduct](CODE_OF_CONDUCT.md) and the workflow in
[CONTRIBUTING.md](CONTRIBUTING.md). No special access is necessary to
contribute.

### Code owners

Trusted contributors who review and approve pull requests. Code ownership is
declared in [`.github/CODEOWNERS`](.github/CODEOWNERS), which GitHub uses to
request reviews automatically.

### Maintainers

Listed in [MAINTAINERS.md](MAINTAINERS.md). Maintainers have merge rights, make
and ratify project decisions, manage releases, and own this governance process.
Maintainers are also code owners.

## Decision-making

The project decides by **lazy consensus**. A proposal — a pull request, an
issue, or a discussion — is accepted if no maintainer raises a blocking
objection inside the review window. For a change that is not trivial, that
window is at least three business days.

When the project does not reach consensus:

- **Majority vote.** Any maintainer can call a vote. A proposal passes on a
  simple majority of the maintainers.
- **Blocking objection.** A maintainer can block a change. The maintainer must
  give a concrete technical reason and a path to resolution.
- **Supermajority decisions.** These changes need a two-thirds supermajority of
  the maintainers: adding a maintainer, removing a maintainer, and changing this
  document.

## Adding and removing maintainers

Any maintainer can nominate a contributor who has had more than one pull request
merged and has shown good judgement. The nomination is accepted on a two-thirds
supermajority vote. A maintainer who is inactive for six months or more can be
moved to emeritus status by the same process.

## Security issues

Security issues follow the process in [SECURITY.md](SECURITY.md). Do not open a
public issue to report a vulnerability.

## Releases

Maintainers cut releases. The process is documented in
[RELEASE.md](RELEASE.md).

## Attribution

Adapted from the governance model in
[NVIDIA/cluster-api-provider-launchlayer](https://github.com/NVIDIA/cluster-api-provider-launchlayer/blob/main/GOVERNANCE.md).
