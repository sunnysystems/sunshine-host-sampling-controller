# Security policy

The Sunshine host-sampling controller runs **inside a customer's Kubernetes
cluster**, so its security posture matters directly to the environments it is
installed in. We take reports seriously.

## Security posture (by design)

- **Fail-open.** If the policy endpoint is unreachable, errors, or returns an
  unconfigured policy, the plan is empty — nothing is sampled and everything stays
  monitored. The controller is never a single point of failure for monitoring.
- **Read-only by default.** In dry-run (the default) the RBAC grants only
  `get/list/watch` on nodes and `get/list` on daemonsets. The `patch/update` verbs
  on nodes are granted **only** when `dryRun=false` (execute).
- **Least privilege.** The only write the controller ever performs is patching the
  `datadog.sunshine/sampled-out` label on nodes. It reads a scoped, read-only
  inbound token from a mounted Secret and never writes back to Sunshine beyond
  best-effort reconcile telemetry.
- **Hardened runtime.** Distroless image, non-root user, read-only root
  filesystem, `allowPrivilegeEscalation: false`, all Linux capabilities dropped,
  `seccompProfile: RuntimeDefault`.

## Supported versions

| Version | Supported |
| ------- | --------- |
| 1.x     | ✅        |
| < 1.0   | ❌ (pre-release, do not run in production) |

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Report privately via **GitHub Private Vulnerability Reporting**:

> Repository → **Security** tab → **Report a vulnerability**
> (https://github.com/sunnysystems/sunshine-host-sampling-controller/security/advisories/new)

<!-- TODO(Sunny): confirmar e preencher um e-mail de contato de segurança
     (ex.: security@<domínio-oficial>) como canal alternativo ao GitHub. -->

Please include:

- affected version / image tag,
- a description of the issue and its impact,
- steps to reproduce (a minimal manifest or config is ideal),
- any suggested remediation.

We aim to acknowledge a report within **3 business days** and to agree on a
disclosure timeline with the reporter. Please give us a reasonable window to ship
a fix before any public disclosure.
