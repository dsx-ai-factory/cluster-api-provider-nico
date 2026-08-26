# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

**This file is generated — do not edit it by hand.** See `cliff.toml`.

## [0.0.42] - 2026-08-26

### Added

- *(ghcr)* Publish the Helm chart to GHCR, split it from the image
- *(security)* Scan release artifacts with ClamAV, and backfill existing tags
- *(ci)* Align quality gates with CAPLL
- *(conditions)* Align v1beta2 model CAPI status aggregation

### Fixed

- *(notices)* Correct licence attribution when vendor/ is present
- *(notices)* Drop the spurious /vN segment from major-version licence URLs
- *(deps)* Bump golang.org/x/text to v0.39.0
- Skip NICo label updates when instance labels are unchanged
- *(release)* Attach assets before publishing, not after
- *(release)* Attach checksums.txt to the release
- Make reconciliation restart-safe

### Documentation

- Add features, architecture and how this relates to other tools

### Maintenance

- Publish NVCR images, Helm chart, and release artifacts from GitHub
- Run CI on Dependabot branches, and add a build that compiles the code
- *(deps)* Catch up the four dependency updates that closed unmerged
- Promote release images by digest instead of rebuilding
- Regenerate THIRD_PARTY_NOTICES.md on Dependabot pull requests
- Move the Go toolchain pin to 1.26.6
- Raise the Go floor to 1.26.6, so CI scans what we ship
- Fail when go.mod is not tidy
- Prepare the checks a merge gate will call
- Add a merge gate, so one check can be required
- Give every commit on main its own CI runs
- *(deps)* Bump github/codeql-action/upload-sarif from 4.37.6 to 4.37.7
- *(deps)* Bump github.com/onsi/ginkgo/v2 from 2.28.1 to 2.32.1
- *(deps)* Bump golang from `640a234` to `0d1d3a7`
- *(deps)* Bump github/codeql-action/init from 4.37.6 to 4.37.7
- *(deps)* Bump github/codeql-action/analyze from 4.37.6 to 4.37.7
- *(deps)* Bump docker/setup-buildx-action from 4.2.0 to 4.3.0
- *(deps)* Consolidate Go dependency updates
- *(lint)* Bump golangci-lint to v2.12.2 so config verify works offline

## [0.0.41] - 2026-08-12

### Documentation

- Point questions at Q&A, and ship licenses in the Helm chart

### Maintenance

- Enforce license headers and dependency licenses
- Verify the container image still builds
- Lint the workflow files
- Enable GHCR image publish on main and tags

## [0.0.40] - 2026-08-12

### Fixed

- *(ci)* Make the SARIF uploads actually run
- *(ci)* De-duplicate govulncheck SARIF tags before upload
- *(notices)* Keep every attribution file, not just the first

### Documentation

- Generate CHANGELOG.md with git-cliff instead of by hand
- Add GOVERNANCE.md
- Add SUPPORT.md

### Maintenance

- Add govulncheck, CodeQL and Grype scans
- Scan the commit history for secrets

