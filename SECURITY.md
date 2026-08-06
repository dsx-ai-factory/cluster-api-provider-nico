# Security Policy: cluster-api-provider-nico

## Reporting a Vulnerability

If you discover a potential security vulnerability, please **do not open a public GitHub issue, pull request, or discussion**.

Report it privately through one of these channels:

1. **Preferred:** [NVIDIA Vulnerability Disclosure Program](https://www.nvidia.com/en-us/security/)
   - Web form: [Security Vulnerability Submission Form](https://www.nvidia.com/object/submit-security-vulnerability.html)
2. **Email:** [psirt@nvidia.com](mailto:psirt@nvidia.com)
   - Use the [NVIDIA public PGP key](https://www.nvidia.com/en-us/security/pgp-key) for encrypted communication.

Include:

- Product or project name and affected version, branch, or commit
- Vulnerability type
- Affected component, file, or interface
- Reproduction steps
- Proof-of-concept code, if available
- Potential impact

Detailed reports help NVIDIA evaluate and address issues faster.

NVIDIA's Product Security Incident Response Team (PSIRT) will acknowledge the report, validate the vulnerability and assess its severity, coordinate development and testing of a fix, and publish a security bulletin when appropriate. Reports are handled under coordinated vulnerability disclosure. NVIDIA does not currently operate a public bug bounty program.

For broader NVIDIA product security policies, see https://www.nvidia.com/en-us/security/psirt-policies/.

## Security Architecture & Context

`cluster-api-provider-nico` (CAPNICo) is a Go-based Kubernetes Cluster API infrastructure provider. It runs as a controller in a management cluster and reconciles `NicoCluster` and `NicoMachine` resources into bare-metal instances managed through the NICo API.

The project is an infrastructure service and privileged control-plane component. Its primary security responsibilities are preserving the authorization and integrity of infrastructure lifecycle operations and protecting NICo credentials and Kubernetes bootstrap data.

**Repository Exposure Classification:** Internal.

Basis: the canonical source remains on NVIDIA's internal GitLab service until public publication is approved.

**Service Exposure Classification:** Internal-Sensitive (high confidence).

Basis: deployments are typically privileged infrastructure controllers that handle NICo credentials, Kubernetes bootstrap data, and machine lifecycle operations; the controller is not intended as an internet-facing service.

The principal security boundaries are:

- **Kubernetes API boundary:** The controllers trust authenticated Kubernetes API requests and authorization decisions for Cluster API objects, CAPNICo custom resources, Secrets, and Machine annotations.
- **Controller service-account boundary:** The controller's ClusterRole permits cluster-wide reads of Secrets and Cluster API resources and permits lifecycle updates to Machines and CAPNICo resources.
- **NICo API boundary:** `internal/nico/client.go` sends authenticated instance, site, VPC, and tenant operations to the endpoint configured by a Kubernetes Secret.
- **Credential boundary:** `internal/nico/config.go` loads a bearer token or OAuth2 client credentials, endpoint details, an optional CA bundle, and the optional `insecureSkipTLSVerify` setting from Secrets.
- **Machine bootstrap boundary:** `controllers/nicomachine_controller.go` reads kubeadm bootstrap data from a Secret and forwards it as instance user data. `NicoMachine.spec.ipxeScript` also controls the instance boot path.
- **Operational endpoint boundary:** The manager exposes health probes and controller-runtime metrics. The default Kustomize configuration serves metrics over HTTP; TLS and NetworkPolicy resources are available but are not enabled by default.
- **Release boundary:** CI builds and publishes multi-architecture images, Cluster API release manifests, and Helm charts using registry credentials and externally downloaded build tools.

### Threat Model

1. **Unauthorized infrastructure lifecycle operations:** An actor able to create or modify `NicoMachine`, `NicoCluster`, their owning Cluster API objects, or the configured Machine annotations can cause instance creation, import, reboot, repair marking, or deletion through `controllers/nicomachine_controller.go`. Kubernetes RBAC and admission controls are therefore part of the provisioning authorization boundary.

2. **Credential theft or API redirection through Secret modification:** A compromised identity Secret can replace the NICo `endpoint`, OAuth `tokenURL`, bearer token, client credentials, CA bundle, or `insecureSkipTLSVerify` setting consumed by `internal/nico/config.go`. This can disclose credentials or redirect privileged API operations. The controller's cluster-wide Secret read permissions increase the impact of controller service-account compromise.

3. **Compromise through bootstrap or iPXE content:** Bootstrap cloud-config from a Machine bootstrap Secret and `NicoMachine.spec.ipxeScript` are forwarded to NICo when an instance is created. Unauthorized modification of either input can execute attacker-controlled startup behavior, expose kubeadm join material, or compromise provisioned nodes.

4. **Cross-cluster or cross-tenant resource confusion:** Clusters without `spec.identityRef` share the provider-level credentials Secret, and NICo clients cache tenant context by Secret revision. Incorrect identity, organization, site, VPC, or imported provider-ID configuration could cause operations to target an unintended administrative scope. Existing-instance recovery also relies on name, VPC, and site lookup after a create conflict.

5. **Sensitive operational data disclosure:** The controller handles bearer tokens, OAuth client secrets, bootstrap cloud-config, machine identifiers, topology information, and TPM-derived identity data. Kubernetes API access, diagnostic output, status fields, and error propagation must not expose Secret contents or confidential bootstrap material. Repair annotation summaries and details are intentionally logged and therefore must not contain credentials or other sensitive data.

6. **Unauthenticated metrics or probe exposure:** `cmd/main.go` binds metrics and health endpoints on configurable addresses. The default deployment exposes HTTP metrics on port 8080, while the provided TLS patch and metrics NetworkPolicy are disabled by default. An overly broad Service or cluster network policy can expose operational metadata or permit resource-exhaustion traffic.

7. **Release pipeline and dependency compromise:** Release pipelines publish images, manifests, and Helm charts using registry credentials. They may rely on shared CI templates and download build tooling such as Kustomize. Compromise of CI dependencies, runners, credentials, registries, or unverified downloads can alter distributed artifacts.

### Critical Security Assumptions

- Kubernetes authentication, RBAC, admission policy, and namespace isolation restrict who may modify Cluster API resources, CAPNICo resources, Machine annotations, and referenced Secrets.
- The controller service account, management-cluster nodes, and Kubernetes control plane are trusted and protected against unauthorized access.
- NICo and OAuth endpoints are trusted, correctly scoped, and reached using validated TLS. Operators do not enable `insecureSkipTLSVerify` except in a deliberately isolated environment with an accepted risk.
- NICo enforces organization and tenant authorization independently of identifiers supplied by CAPNICo.
- Provider-level and per-cluster credential Secrets are encrypted at rest, access-controlled, audited, and rotated when disclosure is suspected.
- Bootstrap Secrets and referenced iPXE content come from trusted controllers and repositories and are protected against unauthorized modification.
- Cluster API owner references, finalizers, provider IDs, and instance identity returned by NICo accurately identify the resources CAPNICo should manage.
- Metrics and probe endpoints are reachable only from authorized management and monitoring networks unless authentication and TLS are enabled.
- CI templates, runners, registries, builder images, downloaded tools, and release credentials are trusted and protected by the release process.
- The host kernel, container runtime, and Kubernetes platform enforce the non-root container and dropped-capability settings in `config/manager/manager.yaml`.

## Trust Model

CAPNICo treats Kubernetes API authorization as its operator-identity boundary. It does not implement an independent end-user authentication layer.

Users authorized to modify CAPNICo or related Cluster API resources may influence infrastructure provisioning. Users authorized to modify Machine reboot or repair annotations may trigger corresponding NICo operations. Users authorized to modify identity or bootstrap Secrets are trusted with the credentials and node initialization data stored in them.

The provider supports two credential scopes:

- A same-namespace Secret selected by `NicoCluster.spec.identityRef`
- A provider-level Secret shared by clusters that do not specify an identity reference

The NICo API remains responsible for authenticating credentials and enforcing organization, tenant, and resource authorization.

## Deployment Assumptions and Hardening

Operators should:

- Restrict writes to `NicoCluster`, `NicoMachine`, Cluster API Machine objects, and lifecycle annotations to trusted automation and administrators.
- Limit Secret access and use per-cluster identities where separate administrative scopes are required.
- Require HTTPS for NICo and OAuth endpoints, supply an appropriate CA bundle when necessary, and avoid `insecureSkipTLSVerify`.
- Protect kubeadm bootstrap Secrets and permit only trusted iPXE script sources.
- Enable the supplied metrics TLS patch and NetworkPolicy, or provide equivalent protection through the deployment platform.
- Ensure management-cluster egress only reaches approved NICo, OAuth, registry, and release endpoints.
- Monitor changes to credential Secrets, infrastructure resources, reboot/repair annotations, and the controller service account.
- Pin and verify CI tooling and release dependencies where the pipeline supports it.

## Security Scope and Common Scanner Context

The following patterns are expected and are not vulnerabilities by themselves:

- Placeholder tokens, client IDs, client secrets, endpoints, and certificates in documentation or tests are non-production examples.
- The link-local metadata-service address shown in kubeadm examples is part of the documented NICo workload boot design.
- Support for a custom CA bundle is intentional and does not disable certificate verification.
- `insecureSkipTLSVerify` is an explicit operator-controlled compatibility option. A report should demonstrate an unauthorized bypass or concrete exposure rather than only note that the option exists.
- The controller's access to bootstrap and identity Secrets is required for its documented function. Reports should demonstrate an authorization bypass, unintended disclosure, or avoidable privilege expansion.

The following remain valid security concerns and should not be dismissed as expected behavior:

- Unauthorized modification or disclosure of production credentials or bootstrap data
- Cross-namespace, cross-cluster, organization, or tenant authorization bypass
- Infrastructure actions that can be triggered without the intended Kubernetes authorization
- External exposure of metrics or probes contrary to the deployment's network policy
- Release artifact tampering or credential leakage in CI/CD
