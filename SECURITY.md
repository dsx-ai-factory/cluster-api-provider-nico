# Security Policy

NVIDIA is dedicated to the security and trust of our software products and
services, including all source code repositories managed through our
organization.

## Reporting a vulnerability

**Report security vulnerabilities to NVIDIA PSIRT.** Do not open a GitHub
issue, a pull request, or a GitHub Security Advisory. NVIDIA product security
policy routes every external vulnerability report through PSIRT so that
disclosure is coordinated across all affected NVIDIA products, not just this
repository.

- **Web:** [Security Vulnerability Submission Form](https://www.nvidia.com/object/submit-security-vulnerability.html)
- **Email:** psirt@nvidia.com — encrypt with the [NVIDIA public PGP key](https://www.nvidia.com/en-us/security/pgp-key)

Include as much of the following as you have:

- The affected version, tag, or commit SHA
- The type of vulnerability: remote code execution, privilege escalation,
  credential exposure, authentication bypass, denial of service, and so on
- Steps to reproduce, and a proof of concept if you have one
- The impact, and how an attacker would reach the vulnerable code

If a security issue is reported publicly by mistake, maintainers will limit
discussion on the public thread and redirect the reporter to PSIRT.

## Response and acknowledgement

PSIRT acknowledges receipt of every report, coordinates with the reporter
through the investigation, and provides progress updates as remediation
proceeds. See [PSIRT Policies](https://www.nvidia.com/en-us/security/psirt-policies/)
for NVIDIA's full coordinated-disclosure policy.

Alongside that process, the maintainers of this repository aim to:

| Stage | Target |
|---|---|
| Acknowledge a report forwarded by PSIRT | 5 business days |
| Confirm or reject, with a severity assessment | 10 business days |
| Give the reporter a remediation timeline | 14 business days |

If you have not heard anything within a few business days of filing, resend to
psirt@nvidia.com rather than opening a public issue.

## Remediation targets

Severity is assessed with CVSS v3.1. These are the targets the maintainers work
to once a report is confirmed; PSIRT owns the disclosure date.

| Severity | CVSS v3.1 | Target fix available |
|---|---|---|
| Critical | 9.0 – 10.0 | 14 calendar days, out-of-band release |
| High | 7.0 – 8.9 | 30 calendar days |
| Medium | 4.0 – 6.9 | 90 calendar days, next scheduled release |
| Low | 0.1 – 3.9 | Next scheduled release |

Dependency vulnerabilities are picked up by Dependabot and follow the same
targets, measured from the date a fixed upstream version becomes available.

## Vulnerability exceptions

Not every finding from Dependabot, govulncheck, or Grype can be fixed by the
target above. When one genuinely cannot — no fixed version exists yet, or
fixing it needs a breaking change out of step with this project's release
cadence — record an exception instead of leaving the finding silently
unaddressed. An exception names:

- The advisory or CVE ID.
- Why it cannot be fixed now.
- An owner.
- An expiry or re-review trigger (a date, or "when `<dependency>` ships a
  fix").

[Grype's `ignore` list](https://github.com/anchore/grype#specifying-matches-to-ignore)
(a `.grype.yaml` in the repository root) is the mechanism once the first
exception is needed; add it with the fields above, not a bare vulnerability
ID. There is no open exception today.

## Embargo and coordinated disclosure

Confirmed vulnerabilities are handled under embargo. PSIRT manages the
timeline: the issue stays confidential while a fix is prepared, and an advisory
is published when the embargo lifts. NVIDIA is a CVE Numbering Authority and
assigns CVE identifiers for resolved issues that require action from users.

Maintainers support this by developing fixes privately and holding public
discussion — issues, pull requests, commit messages and release notes — until
PSIRT lifts the embargo. Please keep anything you report confidential until the
advisory is published.

## Supported versions

Releases are tagged from `main`; see
[Releases](https://github.com/NVIDIA/cluster-api-provider-nico/releases) for
the current list. Per [RELEASE.md](RELEASE.md)'s backport policy, only
critical-severity fixes are backported to an older release — everything else
targets `main` and ships in the next release.

| Version | Supported |
|---|---|
| Latest release | ✅ Receives security fixes |
| `main` | ✅ Receives security fixes |
| Older releases | ⚠️ Critical-severity backports only |
| Release candidates and forks | ❌ Not supported |

## Reporter credit

We credit reporters by name in the published advisory and in the release notes
of the release that carries the fix, unless you ask to stay anonymous. Tell
PSIRT your preference, and the name or handle you want used, when you file.

NVIDIA does not run a bug bounty program. We do acknowledge externally reported
issues resolved under our coordinated vulnerability disclosure policy.

## Artifact integrity

Every release artifact ships with a SHA-256 checksum file (`checksums.txt`,
attached to the GitHub Release) to verify a download against.

**Container images are not yet signed.** Cosign keyless signing is planned
but not implemented. Until it is, verify an image by digest instead of by
tag: compare `bin/crane digest` for the published tag against the digest
recorded in the release body. Third-party dependency licenses are recorded
in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), regenerated and
verified in CI on every push.

## Out of scope

These generally do not qualify as vulnerabilities in this provider:

- Vulnerabilities in upstream components — Cluster API core, controller-runtime,
  cert-manager, Kubernetes itself. Report those to the owning project. If this
  provider's use of one of them makes an upstream issue exploitable in a way it
  otherwise would not be, that **is** in scope.
- Findings that require cluster-admin on the management cluster to exploit.
- Missing hardening that is the operator's responsibility: RBAC, network
  policy, secrets management, webhook TLS configuration.
- Denial of service that needs physical access to the hardware.
- Scanner output with no demonstrated, practical exploit path.
- Reports against a deployment operated by someone else — contact that operator.

When in doubt, report it. PSIRT would rather triage something out of scope than
miss a real issue.

## Guidance for operators

If you deploy or operate this provider:

- **Secrets.** Never commit credentials, API keys, tokens, or kubeconfigs.
  Supply infrastructure credentials through Kubernetes Secrets referenced by the
  provider's resources, not through checked-in manifests.
- **RBAC.** The controller needs broad permissions on its own API group. Do not
  widen the shipped ClusterRole beyond what the chart installs.
- **Network exposure.** Keep the management cluster's API server behind
  authentication and restricted to trusted networks.
- **Updates.** Apply security releases promptly and read the release notes for
  the supported-version policy in force at the time.
- **Dependencies.** Review Dependabot alerts and keep pinned versions current.

## NVIDIA Product Security

For all other security concerns, see NVIDIA's
[Product Security portal](https://www.nvidia.com/en-us/security).
