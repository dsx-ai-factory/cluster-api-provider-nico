# CAPNICo v0.2.0 Vulnerability Assessment

Assessment date: November 24, 2026.
Medium-severity dispositions reviewed: November 25, 2026.

This assessment covers the published controller image, its release source, and
reported container vulnerability findings. It distinguishes runtime impact
from build-environment exposure. The assessment does not depend on a specific
scanner engine.

## Reviewed Artifacts

Repository: `ghcr.io/dsx-ai-factory/cluster-api-provider-nico/controller`.

Image index digest:
`sha256:8c758f5b2693185cd24093becffa3c109c87a68af9c12d53cc3b11eca43ac013`.

| Platform | Manifest digest |
| --- | --- |
| linux/amd64 | `sha256:d81073776eec3a2f0c8e9982d545d2f3dfd7c74d64af84920f7a25cc80c74df2` |
| linux/arm64 | `sha256:a4d49603b27f15f6142219d459b2b71e93b68e2e65f6d75dd4ec2401feac3d95` |

The `/manager` binary was extracted from each manifest. `go version -m`
confirmed Go 1.26.10, `CGO_ENABLED=0`, `golang.org/x/net v0.61.0`,
`google.golang.org/grpc v1.83.1`, and OpenTelemetry SDK v1.45.0 on both platforms.
The release Dockerfile copies the manager and license files into a distroless
runtime image. It does not copy the Go compiler or the go command.

## Findings and Dispositions

### HTML Parser: GO-2026-6860

The [Go advisory](https://pkg.go.dev/vuln/GO-2026-6860), also identified as
CVE-2026-78661, affects HTML tree construction through `Parse`, `ParseFragment`,
and their option variants before `golang.org/x/net v0.62.0`.

The release controller imports `html/charset` through Gomega matchers. That
package calls `html.NewTokenizer`; it does not call the affected tree parser.
`go tool nm` found no `golang.org/x/net/html.Parse*` symbols in either shipped
binary. Source analysis with govulncheck v1.6.0 reported module and package
matches, with no reachable vulnerable symbol for this advisory.

Disposition: `not_affected`, with justification `vulnerable_code_not_present`,
for the two reviewed platform digests. Upgrade x/net separately to remove the
affected module version from future releases.

### gRPC xDS Server: CVE-2026-84445

[GHSA-2v4p-qf9q-27wj](https://github.com/advisories/GHSA-2v4p-qf9q-27wj), also
identified as GO-2026-6443, affects gRPC servers created with
`xds.NewGRPCServer`. CAPNICo uses gRPC as an outbound OpenTelemetry exporter.
The release controller does not import the xDS server package or construct
that server. Neither shipped binary contains xDS server symbols. govulncheck
reported module/package matches without a reachable vulnerable symbol.

Disposition: `not_affected`, with justification `vulnerable_code_not_present`,
for the reviewed digests. Upgrade gRPC separately to v1.83.2 or a later patched
release.

### HTTP/1 Header Race: GO-2026-6859

[GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859), also CVE-2026-97054,
requires a handler to exceed a `MaxBytesReader` limit while reading the request
body concurrently with response-header writes. `maxBytesReader.Read` is
present in both shipped binaries, so code absence is not the justification.

The controller-runtime v0.25.2 admission webhook uses `io.LimitedReader` and
reads the body synchronously before writing its response. Inspection of the
controller dependency closure found no non-standard-library references to
`MaxBytesReader` or `MaxBytesHandler`. The remaining standard-library
`OPTIONS *` path sets its header and drains the limited body synchronously.
No handler path with the required concurrency pattern was identified.

Disposition: `not_affected`, with justification
`vulnerable_code_not_in_execute_path`, for the reviewed digests. govulncheck
reports reachable generic HTTP symbols for this advisory; that alone does
not demonstrate its concurrency prerequisite. Go 1.27.4 fixes the issue in
future builds. Re-review if HTTP handlers or body-limit middleware change.

### HTML Renderer: GO-2026-6866

[GO-2026-6866](https://pkg.go.dev/vuln/GO-2026-6866), also CVE-2026-97045,
affects `html.Render` and its foreign-namespace handling before x/net v0.62.0.
Neither shipped binary contains `html.Render` or `childTextNodesAreLiteral`
symbols. The release uses `html.NewTokenizer` through `html/charset`, rather
than rendering HTML trees. Source and binary govulncheck results identify
module/package presence without an affected rendering symbol.

Disposition: `not_affected`, with justification `vulnerable_code_not_present`,
for the reviewed digests. The existing x/net v0.62.0 upgrade fixes future
builds.

### W3C Baggage Parsing: GO-2026-6975

[GO-2026-6975 / GHSA-rrfc-gjm9-cgrj](https://github.com/open-telemetry/opentelemetry-go/security/advisories/GHSA-rrfc-gjm9-cgrj)
concerns bounded allocation amplification when parsing attacker-controlled
W3C baggage. It does not establish unlimited allocation or reliable process
termination.

CAPNICo explicitly installs `propagation.TraceContext{}` in
`internal/telemetry/tracing.go`, without a Baggage propagator. Its HTTP
instrumentation wraps outbound requests. Neither shipped binary contains
`baggage.Parse` or `propagation.Baggage.Extract` symbols. govulncheck reports
no reachable affected parsing symbol.

Disposition: `not_affected`, with justification
`vulnerable_code_not_in_execute_path`, for the reviewed digests. Re-review if
baggage propagation or extraction is introduced. The existing OpenTelemetry
v1.47.0 pins fix future builds.

### Go Build Tools

These eight reports affect the go command, compiler, or their interactions
with host compiler/linker inputs when building malicious packages.

| CVE | Go advisory | Affected operation |
| --- | --- | --- |
| CVE-2026-97043 | [GO-2026-6848](https://pkg.go.dev/vuln/GO-2026-6848) | Escaped quotes in cgo linker directives. |
| CVE-2026-97039 | [GO-2026-6852](https://pkg.go.dev/vuln/GO-2026-6852) | pkg-config argument validation. |
| CVE-2026-97038 | [GO-2026-6853](https://pkg.go.dev/vuln/GO-2026-6853) | Object files and external linker inputs. |
| CVE-2026-97037 | [GO-2026-6854](https://pkg.go.dev/vuln/GO-2026-6854) | Split linker flags and linker scripts. |
| CVE-2026-97036 | [GO-2026-6855](https://pkg.go.dev/vuln/GO-2026-6855) | Darwin linker plugin loading. |
| CVE-2026-97035 | [GO-2026-6856](https://pkg.go.dev/vuln/GO-2026-6856) | Compiler flag validation. |
| CVE-2026-97034 | [GO-2026-6857](https://pkg.go.dev/vuln/GO-2026-6857) | GCC spec-file handling. |
| CVE-2026-97033 | [GO-2026-6858](https://pkg.go.dev/vuln/GO-2026-6858) | Compiler/linker paths and input languages. |

The final controller image does not ship or invoke those build tools. Its
embedded Go version identifies the builder; it does not place `cmd/go` or
`cmd/compile` into the controller executable. Both binary build records also
confirm that cgo is disabled. These facts support `not_affected` with
`vulnerable_code_not_present` for the shipped runtime images. They do not
disposition the builder environment or establish that an arbitrary build is
safe. Upgrade the builder independently to Go 1.26.11 or Go 1.27.4 or later.

## Why Container Scans and Dependabot Differ

| Source | Observed input and behavior | Limits of the finding |
| --- | --- | --- |
| Container vulnerability report | Identifies versions inside released `/manager` binaries and reports a row for each platform. | A version match does not establish that the affected function or build tool is present or reachable. The two platform rows still identify distinct image manifests. |
| Dependabot alerts | Matches the repository dependency graph to reviewed GitHub advisories with package/version mappings. | It does not prove runtime reachability or that a published image has been rebuilt after a dependency changes on main. |
| Dependabot update PRs | Proposes version and Docker image updates, including routine updates without a security alert. | An open update PR is not equivalent to an open vulnerability alert or a published fix. |
| govulncheck | Reports module, package, and symbol-level evidence from Go source or binaries. | Source results depend on the selected entry point, platform, build tags, toolchain, and current advisory database. Binary results do not provide the same source call graph. |

At assessment time, two reviewed GitHub advisories matched the repository's
dependencies:

- High [GHSA-2v4p-qf9q-27wj](https://github.com/advisories/GHSA-2v4p-qf9q-27wj),
  the gRPC xDS advisory assessed above.
- Medium [GHSA-q7gw-24fr-5p69](https://github.com/advisories/GHSA-q7gw-24fr-5p69),
  also CVE-2026-128085, for OpenTelemetry versions before v1.47.0. The source
  update includes the upstream fix. This document makes no `not_affected`
  claim for that advisory. Its absence from a High-only report would not
  establish a scanner miss.

GitHub's advisory API returned unreviewed entries with empty vulnerability
package mappings for CVE-2026-78661 and all eight build-tool CVEs. This helps
explain their absence from Dependabot alerts. For example, the HTML issue is
[GHSA-75pg-wvh3-34jr](https://github.com/advisories/GHSA-75pg-wvh3-34jr).
These observations describe this dated snapshot, not a permanent coverage
guarantee for either tool.

## Remediation and Consumption

The source update aligns `go.mod`, both Dockerfiles, and contributor
instructions on Go 1.27.4. It upgrades x/net to v0.62.0, gRPC to v1.83.2, and
OpenTelemetry SDK to v1.47.0, including the SDK metric module checksum. The
shared API, trace, and metric modules were already at v1.47.0 on main.
These changes need a new release before the
published controller image receives them. Do not overwrite the v0.2.0 tag.

[`.openvex.json`](../.openvex.json) records thirteen statements against the two
reviewed manifest digests. It does not claim that all findings are benign or
that the old image is free from other vulnerabilities. It contains no
disposition for GHSA-q7gw-24fr-5p69.

Before applying this document, ensure the consumer enforces product digest
scope. Match each statement to the exact platform manifest in the table
above. A repository basename alone is insufficient to establish that a
disposition applies. Complete the consuming system's review process before
applying dispositions, and do not reuse them for arbitrary future releases.

Signed VEX attestations and publishing a new release are separate operations.

## Validation of the Source Update

The required code and manifest generation, formatting, race-enabled tests,
native build, and notice regeneration completed successfully. Regeneration
produced no tracked code or manifest changes. The OpenVEX validator passed,
and its rejection tests covered missing scope, evidence, and invalid values.

The patched controller was cross-built with `CGO_ENABLED=0` for linux/amd64
and linux/arm64 using Go 1.27.4. Grype v0.112.0 reported zero matches for both
binary directories. govulncheck v1.6.0 reported no findings for either binary
or the Linux controller source entry point. These are dated scan results,
not a guarantee against vulnerabilities disclosed later.

Docker is unavailable in the local validation environment. The full container
builds and Kind end-to-end suite require CI. Binary scans do not cover the
distroless base image or replace those checks.
