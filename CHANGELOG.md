# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

**This file is generated — do not edit it by hand.** See `cliff.toml`.

## [Unreleased]

### Added

- Generate SPDX SBOM in Image workflow

### Fixed

- Keep redirect comment body inside the YAML block scalar
- *(notices)* Pin collation so the generated file is the same everywhere

### Documentation

- Expand AGENTS.md with permitted work, out-of-scope, secrets, verification, and examples
- Add development environment setup section to CONTRIBUTING.md
- Add supported versions table to SECURITY.md
- Add MAINTAINERS.md
- Remove TerryHowe from maintainers list
- Add commit format and changelog maintenance to CONTRIBUTING.md
- Add CI, license, and release badges to README
- Add THIRD_PARTY_NOTICES.md with a generator and CI check
- Route vulnerability reports to PSIRT and complete the security policy
- Add a root NOTICE file
- Adopt the Contributor Covenant 2.1 Code of Conduct
- Add a docs/ directory and fix the README
- Add a reading map to AGENTS.md
- Generate CHANGELOG.md with git-cliff instead of by hand

### Maintenance

- Make GitLab CI release-only with GitHub redirect
- Skip GitLab release pipelines for non-release changes
- Add RELEASE.md and automated GitHub release workflow
- Expand CODEOWNERS with per-directory ownership entries
- Pin Dockerfile base image digests and add fork PR gate workflow
- Add workflow_call trigger and wire fork PR head ref to test/lint
- Enable copy-pr-bot
- Run every check on the self-hosted runners
- Use the runner BuildKit config for image builds
- Ship LICENSE, NOTICE and THIRD_PARTY_NOTICES.md in the image
- Use @NVIDIA/nke-write in CODEOWNERS
- Add DCO configuration, sign-off docs, and an AI contribution policy

### Other

- Initial public commit of cluster-api-provider-nico.
- Bump golang.org/x/net from 0.49.0 to 0.55.0


