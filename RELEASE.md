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
| `ghcr.io/nvidia/cluster-api-provider-nico/controller` | ⚠️ see below | tagged versions only, release candidates included |
| `ghcr.io/nvidia/cluster-api-provider-nico/charts` | ⚠️ see below | the Helm chart, as `capi-provider-nico` |

⚠️ **GHCR packages are not public yet.** This repository is `internal`, and a new
GHCR package inherits that visibility rather than taking it from the registry.
Repository visibility does not change it either — each package has to be made
public in its own settings. **Until that is done, "GHCR" here means "reachable
without an NGC login", not "reachable anonymously."** The same applies to the
existing image package.

The chart is published twice, once per registry, because a chart has to name an
image its reader can reach: the NGC copy names the DSX image, the GHCR copy names
the GHCR image. `charts/` is a convention this repository follows, not something
the registry enforces — the separation holds because the workflow pushes the
image to `<repo>/controller` and the chart to `<repo>/charts`, and it would stop
holding if either changed.

The NKE org is the promotion source; the DSX org is where internal consumers
point. GHCR receives only the multi-arch index for a tag, so the
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

1. **Preview the release notes** with `make changelog` if desired. The preview
   uses [git-cliff](https://git-cliff.org), configured in `cliff.toml`, and
   includes changes since the latest stable tag. GitHub generates the published
   notes from merged pull requests since the preceding stable tag when the
   release workflow runs. RC and final release notes are therefore cumulative
   for the version. The preview is advisory and `CHANGELOG.md` is not updated.
   `git-cliff` must be installed and available on `PATH` to run the preview.

2. **Update `metadata.yaml`** if the major or minor version is new, or if the
   Cluster API contract version supported by this release differs from the previous one.

3. **Ensure the intended release commit is on `main`** and its required checks
   have passed. If step 2 required a change, commit it and merge it through a
   pull request before continuing.

4. **Tag the release** on the merge commit. Normally this is the promotion of a
   tested release candidate, as described below. To tag a final release
   directly:
   ```bash
   git tag -s v<VERSION> -m "Release v<VERSION>"
   git push upstream v<VERSION>
   ```

5. **The Release workflow runs**, in this order:
   - **Tag NVCR** repoints `<tag>` at the `sha-<commit>` digest in both NGC
     orgs. Nothing is uploaded.
   - **Promote to GHCR** copies that digest to
     `ghcr.io/nvidia/cluster-api-provider-nico/controller:<tag>`, plus the
     matching `sha-<commit>` tag, and records the digest in the job summary.
   - **SBOM** generates an SPDX SBOM per architecture, addressing each one by
     digest, and uploads them as workflow artifacts.
   - **Chart (NGC)** packages the native `capi-provider-nico` chart with
     `manager.image.repository` pointing at the DSX image and pushes it to the
     NGC chart registry under `DSX_NGC_ORG`/`DSX_NGC_TEAM`.
   - **Chart (NKE)** packages the same native chart with the NKE image and the
     `svc-nke-ngc-imagepull` default, then pushes it to
     `oci://nvcr.io/j7sbcjl3qgta`.
   - **Chart (GHCR)** packages the chart with its image repository pointing at
     the promoted GHCR image and pushes it to
     `oci://ghcr.io/nvidia/cluster-api-provider-nico/charts`. It fails if the
     rendered manager still names an NVCR image, because that failure would
     otherwise surface only at install time.
   - **Create GitHub Release** creates the release as a **draft**, asks GitHub
     to generate notes from merged pull requests, and attaches the promoted
     digest and the clusterctl provider artifacts `metadata.yaml` and
     `infrastructure-components.yaml`. **Publish the release** then checks both
     assets are present and publishes it. They run last so the release never
     advertises an image that has not landed yet.

     It is published in two steps because GitHub's **release immutability**
     seals a release the moment it publishes — so a release published before
     its assets upload can never receive them. Immutability is off here today,
     but it is on in `cluster-api-addon-provider-syscmp`, where a release
     shipped with no assets at all. Draft-first also fails safe: a broken run
     leaves an **unpublished draft**, not a broken public release.

     > ⚠️ **If a release run fails, check for a leftover draft** under
     > [Releases](../../releases) and delete it before re-pushing the tag.
     > Re-running the failed job also works — it finds the draft, re-uploads,
     > and publishes.

   The jobs that reach NVCR are restricted to `NVIDIA/cluster-api-provider-nico`,
   and the GHCR promotion reads from NVCR, so the workflow no-ops in a fork —
   including the GitHub Release, which depends on that promotion. Destinations
   and credentials come from the `nvcr` environment: the variables
   `NVCR_NKE_IMAGE`, `NVCR_DSX_IMAGE`, `DSX_NGC_ORG` and `DSX_NGC_TEAM`, and the
   secrets `NVCR_NKE_AUTH_TOKEN` and `NGC_REGISTRY_TOKEN_DSX`. A missing variable
   fails the job with a named error rather than pushing somewhere unintended.

6. **Verify**
   - GitHub Release: `https://github.com/NVIDIA/cluster-api-provider-nico/releases`
   - GHCR image: `ghcr.io/nvidia/cluster-api-provider-nico/controller:v<VERSION>`
   - GHCR chart: `helm pull oci://ghcr.io/nvidia/cluster-api-provider-nico/charts/capi-provider-nico --version <VERSION>`
   - NVCR images: `<NVCR_NKE_IMAGE>:v<VERSION>` and `<NVCR_DSX_IMAGE>:v<VERSION>`
   - NGC chart: `ngc registry chart info <DSX_NGC_ORG>/<DSX_NGC_TEAM>/capi-provider-nico`
   - NKE chart: `helm pull oci://nvcr.io/j7sbcjl3qgta/capi-provider-nico --version <VERSION>`
   - The digests agree, which is the point of promoting rather than rebuilding:

     ```bash
     make crane
     bin/crane digest ghcr.io/nvidia/cluster-api-provider-nico/controller:v<VERSION>
     bin/crane digest <NVCR_NKE_IMAGE>:v<VERSION>
     ```

## Release candidates

Tag and push a candidate from the commit you intend to release:

```bash
make release-rc VERSION=v0.1.0-rc.1
```

The target is a small wrapper around `git tag -s` and `git push`. It pushes to
`origin` by default; set `RELEASE_REMOTE` to use another Git remote.

A candidate takes the identical path a release does — same digest promoted to the
same registries, a public GitHub prerelease, and clusterctl artifacts you can
`clusterctl init` from. Iterate with `-rc.2` and so on. When the candidate is
good, promote it:

```bash
make promote-rc VERSION=v0.1.0-rc.1
```

This derives `v0.1.0`, tags the commit referenced by `v0.1.0-rc.1`, and pushes
the final tag. The release is therefore the same digest under a new name even
if the candidate commit is no longer checked out.

Candidate tags are kept, not deleted, so `v<VERSION>-rc.1` and `v<VERSION>` both
remain resolvable and, on the same commit, identical.

## Backport policy

Only critical security fixes are backported. All other fixes target `main`.

## Who can release

Any [maintainer](MAINTAINERS.md) may cut a release.
