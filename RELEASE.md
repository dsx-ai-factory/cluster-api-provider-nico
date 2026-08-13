# Release Process

This document describes how to cut a release of `cluster-api-provider-nico`.

## Versioning

This project uses [Semantic Versioning](https://semver.org/):

- **MAJOR** — incompatible API or CRD changes that require migration (e.g. removing a field from `NicoCluster.spec`)
- **MINOR** — new backward-compatible features (e.g. new optional spec fields, new CRDs)
- **PATCH** — backward-compatible bug fixes

A **breaking change** is any change that requires users to update their YAML manifests,
credentials Secrets, or `clusterctl` configuration before upgrading.

## Release cadence

Releases are cut on demand. There is no fixed schedule.

## Registries and promotion

Nothing is built for a release. Every commit merged to `main` is built once and
published to NVCR as `sha-<commit>`, and tagging a release renames that exact
digest and copies it to GHCR. A release therefore ships the image that has
already been running as `edge`, byte for byte, and `v1.2.3-rc.1` and `v1.2.3` are
the same image whenever they name the same commit.

| Registry | Audience | Receives |
| --- | --- | --- |
| NVCR, at `NVCR_NKE_IMAGE` and `NVCR_DSX_IMAGE` | NVIDIA-internal | every `main` commit as `sha-<commit>`, `edge` and `latest`, plus every tag |
| `ghcr.io/nvidia/cluster-api-provider-nico` | public | tagged versions only, release candidates included |

The NKE org is the promotion source; the DSX org is where internal consumers and
the Helm chart point. GHCR receives only the multi-arch index for a tag, so the
per-arch tags a build produces stay internal, and untagged commits never become
public at all. Its path is derived from the repository rather than written into
the workflow, so renaming or moving the repository moves the published image with
it.

The one constraint this imposes is that **a tag must point at a commit that was
built on `main`.** Tagging anything else fails with an error naming the missing
`sha-<commit>`, rather than quietly building something that was never tested.

Promotion uses [crane](https://github.com/google/go-containerregistry), pinned in
the `Makefile` as `CRANE_VERSION` and built by `make crane`, so a maintainer can
run the same copy by hand. Kubernetes promotes with `kpromo` instead, but its
provider-side command only opens a pull request against `kubernetes/k8s.io` for
the central promoter to act on, which has no equivalent here.

## Steps to release

1. **Update `CHANGELOG.md`.** It is generated from commit subjects by
   [git-cliff](https://git-cliff.org), configured in `cliff.toml`. **Do not
   edit it by hand.**

   ```bash
   # 1. Drop the pending [Unreleased] section. Without this, --prepend leaves it
   #    in place holding the same entries the new section just claimed, so the
   #    file lists everything twice and keeps a stale [Unreleased] forever.
   awk '/^## \[Unreleased\]/{s=1} /^## \[[0-9]/{s=0} !s' CHANGELOG.md > /tmp/cl && mv /tmp/cl CHANGELOG.md
   
   # 2. Prepend the new section. --prepend leaves published sections byte-identical;
   #    regenerating the whole file re-derives their dates and rewrites history.
   git-cliff --unreleased --tag v<VERSION> --prepend CHANGELOG.md
   ```
   
   Run this in a **full clone**. On a shallow one git-cliff emits a near-empty
   section and exits 0, so nothing fails.

   The heading carries **no `v` prefix**, which is what the release workflow
   expects when it reads the section back with `awk "/^## \[${VERSION}\]/"` —
   and it falls back to a placeholder rather than failing, so a mismatch ships
   empty notes.

2. **Update `metadata.yaml`** if the major or minor version is new, or if the
   Cluster API contract version supported by this release differs from the previous one.

3. **Commit and open a PR** against `main`:
   ```bash
   git commit -s -m "chore: prepare release v<VERSION>"
   ```
   Merge after review.

4. **Tag the release** on the merge commit:
   ```bash
   git tag -s v<VERSION> -m "Release v<VERSION>"
   git push upstream v<VERSION>
   ```

5. **The Release workflow runs**, in this order:
   - **Tag NVCR** repoints `<tag>` at the `sha-<commit>` digest in both NGC
     orgs. Nothing is uploaded.
   - **Promote to GHCR** copies that digest to
     `ghcr.io/nvidia/cluster-api-provider-nico:<tag>`, plus the matching
     `sha-<commit>` tag, and records the digest in the job summary.
   - **SBOM** generates an SPDX SBOM per architecture, addressing each one by
     digest, and uploads them as workflow artifacts.
   - **Helm chart (dsx)** packages `capi-provider-nico` and pushes it to the NGC
     chart registry under `DSX_NGC_ORG`/`DSX_NGC_TEAM`, with the chart's manifest
     pointing at the DSX image.
   - **Create GitHub Release** publishes notes extracted from `CHANGELOG.md`,
     the promoted digest, and the clusterctl provider artifacts `metadata.yaml`
     and `infrastructure-components.yaml`. It runs last so the release never
     advertises an image that has not landed yet.

   The jobs that reach NVCR are restricted to `NVIDIA/cluster-api-provider-nico`,
   and the GHCR promotion reads from NVCR, so the workflow no-ops in a fork —
   including the GitHub Release, which depends on that promotion. Destinations
   and credentials come from the `nvcr` environment: the variables
   `NVCR_NKE_IMAGE`, `NVCR_DSX_IMAGE`, `DSX_NGC_ORG` and `DSX_NGC_TEAM`, and the
   secrets `NVCR_NKE_AUTH_TOKEN` and `NGC_REGISTRY_TOKEN_DSX`. A missing variable
   fails the job with a named error rather than pushing somewhere unintended.

6. **Verify**
   - GitHub Release: `https://github.com/NVIDIA/cluster-api-provider-nico/releases`
   - GHCR image: `ghcr.io/nvidia/cluster-api-provider-nico:v<VERSION>`
   - NVCR images: `<NVCR_NKE_IMAGE>:v<VERSION>` and `<NVCR_DSX_IMAGE>:v<VERSION>`
   - NGC chart: `ngc registry chart info <DSX_NGC_ORG>/<DSX_NGC_TEAM>/capi-provider-nico`
   - The digests agree, which is the point of promoting rather than rebuilding:

     ```bash
     make crane
     bin/crane digest ghcr.io/nvidia/cluster-api-provider-nico:v<VERSION>
     bin/crane digest <NVCR_NKE_IMAGE>:v<VERSION>
     ```

## Release candidates

Tag a candidate on the commit you intend to release:

```bash
git tag -s v<VERSION>-rc.1 -m "Release candidate v<VERSION>-rc.1"
git push upstream v<VERSION>-rc.1
```

A candidate takes the identical path a release does — same digest promoted to the
same registries, a GitHub prerelease, and clusterctl artifacts you can
`clusterctl init` from. Iterate with `-rc.2` and so on.

When a candidate is good, release it by tagging the commit it points at:

```bash
git tag -s v<VERSION> 'v<VERSION>-rc.1^{}' -m "Release v<VERSION>"
git push upstream v<VERSION>
```

`^{}` dereferences the candidate tag to its commit; without it git creates a
nested tag, one tag pointing at another, rather than a second name for the same
commit. Quote it — `^` and `{}` are shell metacharacters. Tagging the commit
directly works just as well; naming the candidate only records which one was
promoted.

Because the release promotes whatever `sha-<commit>` names, releasing the
candidate's commit ships the digest that was tested. Tagging a later commit is
allowed and still promotes rather than builds, but it is no longer the artifact
the candidate validated.

Candidate tags are kept, not deleted, so `v<VERSION>-rc.1` and `v<VERSION>` both
remain resolvable and, on the same commit, identical.

## Backport policy

Only critical security fixes are backported. All other fixes target `main`.

## Who can release

Any [maintainer](MAINTAINERS.md) may cut a release.
