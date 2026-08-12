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

5. **Three workflows fire on the tag push** (independent; none can break the others):
   - **Release** creates a GitHub Release with notes extracted from
     `CHANGELOG.md` and attaches the clusterctl provider artifacts,
     `metadata.yaml` and `infrastructure-components.yaml`. The attached manifest
     references the GHCR image for that tag.
   - **Image** builds `linux/amd64` and `linux/arm64`, uploads SPDX SBOM
     artifacts, and pushes a multi-arch manifest to
     `ghcr.io/nvidia/cluster-api-provider-nico:<tag>` (plus the matching
     `sha-<commit>` tag). Pushes to `main` publish `:edge` the same way.
   - **NVCR** pushes the same image tags to both NGC orgs,
     `nvcr.io/j7sbcjl3qgta/cluster-api-provider-nico` and
     `nvcr.io/0837451325059433/components-dev/cluster-api-provider-nico`, adding
     `:latest` on `main`. On a tag it also packages the `capi-provider-nico`
     Helm chart and pushes it to the NGC chart registry at
     `0837451325059433/components-dev/capi-provider-nico`, with the chart's
     manifest pointing at the DSX image.

   The NVCR workflow needs the `nvcr` environment and is skipped outside
   `NVIDIA/cluster-api-provider-nico`, so forks can disable or ignore it without
   affecting the GHCR publish. Each workflow rebuilds from source rather than
   copying the GHCR digest.

6. **Verify**
   - GitHub Release: `https://github.com/NVIDIA/cluster-api-provider-nico/releases`
   - GHCR image: `ghcr.io/nvidia/cluster-api-provider-nico:v<VERSION>`
   - NVCR images: `nvcr.io/j7sbcjl3qgta/cluster-api-provider-nico:v<VERSION>` and
     `nvcr.io/0837451325059433/components-dev/cluster-api-provider-nico:v<VERSION>`
   - NGC chart: `ngc registry chart info 0837451325059433/components-dev/capi-provider-nico`

## Backport policy

Only critical security fixes are backported. All other fixes target `main`.

## Who can release

Any [maintainer](MAINTAINERS.md) may cut a release.
