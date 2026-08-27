# Contributing to cluster-api-provider-nico

## Development environment setup

**Prerequisites:**

- Go 1.24+ (see `go.mod` for the exact version in use)
- Docker (for building the controller image and running e2e tests)
- [kind](https://kind.sigs.k8s.io/) — local Kubernetes cluster for integration testing
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [clusterctl](https://cluster-api.sigs.k8s.io/user/quick-start#install-clusterctl) — for generating cluster manifests

**Clone and build:**

```bash
git clone https://github.com/NVIDIA/cluster-api-provider-nico.git
cd cluster-api-provider-nico

# Generate deepcopy methods and CRD manifests
make generate manifests

# Build the controller binary
make build
```

**Run unit tests:**

```bash
make test
```

Controller tests use file-backed Kubernetes and NICo state. See
[docs/writing-tests.md](docs/writing-tests.md), and use `make test-update` when
the expected state intentionally changes.

**Run the full local CI gate** (mirrors what runs in GitHub Actions):

```bash
make generate manifests fmt test build
```

**Run the controller locally** (requires a running cluster with CAPI installed):

```bash
export KUBECONFIG=/path/to/management-cluster.kubeconfig
make run
```

**Explore available targets:**

```bash
make help
```

---

Thanks for your interest in contributing. Contributions typically fall into
three categories:

1. Report a bug, feature request, or documentation issue
   - File an [issue](https://github.com/NVIDIA/cluster-api-provider-nico/issues/new/choose)
     describing what you encountered or what you want changed.
   - Include provider version or commit, Cluster API / Kubernetes versions, and
     relevant controller logs (with secrets redacted).
2. Propose a new feature and implement it
   - Open an issue first so design and scope can be discussed.
   - Once there is agreement, follow the [code contributions](#code-contributions)
     guide below.
3. Implement a fix or feature for an existing issue
   - Comment on the issue to claim it, then follow the guide below.

## Code contributions

### Your first change

1. Read [README.md](README.md) for install and development context.
2. Find an issue labeled
   [good first issue](https://github.com/NVIDIA/cluster-api-provider-nico/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22)
   or
   [help wanted](https://github.com/NVIDIA/cluster-api-provider-nico/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22).
3. Comment on the issue saying you intend to work on it.
4. Fork the repository and create a branch from `main`.
5. Make your change and update or add tests.
6. Run local checks:

```bash
make generate
make manifests
make fmt
make test
make build
```

7. Open a [pull request](https://github.com/NVIDIA/cluster-api-provider-nico/compare)
   against `main`.
8. Ensure CI status checks pass, or fix failures.
9. Address review feedback. A maintainer will merge when approved.

### Pull request expectations

- Prefer small, focused PRs.
- Keep API and behavior changes explicit in the PR description.
- Do not include credentials, kubeconfigs, or other secrets in logs, examples,
  or fixtures.
- Update docs or examples when user-facing behavior changes.
- Follow the existing Go style and package layout (`controllers/`,
  `internal/nico/`, `api/`).

### Branch naming

Use a short descriptive branch name, for example:

- `feat/machine-topology-labels`
- `fix/identity-secret-rotation`
- `docs/contributing-quickstart`

## Commit message format

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short description>

[optional body]

[optional footer: Closes #<issue>]
```

Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`, `perf`

Examples:
```
feat(api): add NicoClusterTemplate status conditions
fix(controllers): handle nil identityRef on cluster delete
docs: update credential rotation guidance
chore(deps): bump golang.org/x/net to v0.55.0
```

Breaking changes must include `BREAKING CHANGE:` in the footer or `!` after the type:
```
feat!: remove deprecated v1alpha1 NicoCluster fields

BREAKING CHANGE: spec.legacyEndpoint is removed. Use spec.identityRef instead.
```

### Sign your commits (DCO)

Commits carry a `Signed-off-by:` trailer. Add one with `-s`:

```bash
git commit -s -m "fix(controller): retry instance lookup on conflict"
```

That appends a line matching the name and email in your Git configuration:

```
Signed-off-by: Jane Developer <jane@example.com>
```

The sign-off is your statement that you have the right to submit the work under
this repository's license. It is the [Developer Certificate of
Origin](https://developercertificate.org/), the same mechanism the Linux kernel
and Kubernetes use.

**Organization members are relaxed, not exempt.** The check skips a commit only
when the author is an NVIDIA organization member **and** GitHub reports the
commit as verified-signed. An unsigned commit from a member with no sign-off
still fails, reporting "Commit by organization member is not verified". Signing
your commits (`-S`) is what makes the exemption apply.

If you forget the `-s`, the simplest fix is to add it to every commit on the
branch:

```bash
git rebase --keep-base --signoff main
git push --force-with-lease
```

`--keep-base` matters: without it the rebase quietly moves your branch onto
whatever your local `main` points at, which on a fork is usually stale.

Remediation without rewriting history is also allowed, but it is not simply an
empty `-s` commit. The app looks for one exact sentence per unsigned commit,
each naming that commit's full SHA:

```
I, Jane Developer <jane@example.com>, hereby add my Signed-off-by to this commit: 3f53ada...

Signed-off-by: Jane Developer <jane@example.com>
```

The failing check prints the exact text to copy, so take it from there rather
than typing it by hand. Note the app itself advises against `--allow-empty` for
this, because an empty commit is dropped if anyone rebases the branch.

The configuration lives in [`.github/dco.yml`](.github/dco.yml).

## AI-assisted contributions

We welcome contributions written with the help of AI coding assistants (Claude,
Copilot, Cursor, and so on). When using AI assistance:

- **You are responsible** for reviewing and understanding all AI-generated code
  before submitting it. Do not submit code you cannot explain or defend in review.
- **Run the full local gate**, not just the tests: `make generate manifests fmt
  test build`. An assistant will usually run the tests and skip regeneration,
  which is what leaves `config/crd/bases/` stale.
- **Say so in the pull request description** if a substantial part of the change
  was generated, so reviewers can calibrate how closely to read it.
- **Point your assistant at [AGENTS.md](AGENTS.md)** first. It carries the rules
  that are not inferable from the surrounding code.

Two of those rules matter most here, because this provider manages real NICo
instances: hold the `NicoMachine` finalizer while `status.instanceID` may still
refer to a live instance, and keep auth and connection state in the client
layer rather than in CR status. Getting the first wrong leaks an instance that
nothing will clean up.

## Changelog

**Do not edit `CHANGELOG.md`.** It is generated from commit subjects by
[git-cliff](https://git-cliff.org), configured in `cliff.toml`, and a new
section is prepended when a release is cut.

Your commit subject is the changelog entry, so write it for a reader. The
conventional-commit type picks the section: `feat` → Added, `refactor` →
Changed, `fix` → Fixed, `perf` → Performance, `docs` → Documentation,
`ci`/`build`/`chore`/`test`/`style` → Maintenance, `revert` → Reverted. A
subject matching no type still appears, under Other. A breaking change — `!`
after the type, or a `BREAKING CHANGE:` footer — is marked in the entry.

This replaces the previous convention of adding an entry by hand in the same
pull request. That did not scale: every open pull request appended to the same
`### Added` list, so each conflicted with the next and whichever landed first
forced a rebase on all the others.

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Security

Do not file public issues for security vulnerabilities. See [SECURITY.md](SECURITY.md).

## Attribution

Portions adapted from the NVIDIA PLC-OSS-Template and
https://github.com/pytorch/pytorch/blob/master/CONTRIBUTING.md.
