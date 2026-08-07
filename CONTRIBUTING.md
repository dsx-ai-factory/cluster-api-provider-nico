# Contributing to cluster-api-provider-nico

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

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Security

Do not file public issues for security vulnerabilities. See [SECURITY.md](SECURITY.md).

## Attribution

Portions adapted from the NVIDIA PLC-OSS-Template and
https://github.com/pytorch/pytorch/blob/master/CONTRIBUTING.md.
