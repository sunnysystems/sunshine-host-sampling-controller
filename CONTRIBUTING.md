# Contributing & support

Thanks for looking at the Sunshine host-sampling controller. This project is
open-source (Apache-2.0) primarily so that **the teams who run it in their own
clusters can audit exactly what it does** — a controller that patches node labels
and pulls the Datadog agent off nodes should be readable, not a black box.

## Support model — please read first

**Issues: yes. External pull requests: no.**

- ✅ **Open an issue** for bug reports, questions, unexpected behavior, or feature
  requests. This is the right channel and we monitor it.
- ✅ **Fork and audit** freely. Build it, read it, run it against your own cluster,
  and tell us what you find.
- ❌ **We do not accept external pull requests.** This is a client-side artifact
  maintained by Sunny; its canonical source is developed internally and mirrored
  here, and it carries a versioned API contract with the Sunshine platform. We
  can't merge drive-by code changes without breaking that pipeline. If you have a
  fix in mind, please **open an issue describing it** — we'll implement and
  attribute it.

Security vulnerabilities go through a private channel — see
[`SECURITY.md`](SECURITY.md). Do not file them as public issues.

## Filing a good issue

Include the controller version / image tag, whether you're in `dryRun` or execute,
the relevant policy fields (`surgeSamplePct`, `floorNodes`, the selectors), the
controller logs around the event, and the metrics from `:9090/metrics` if
relevant. A minimal reproduction (manifest / values) helps a lot.

## Building and testing locally (for auditors)

Building locally requires **Go 1.25+**; `make docker` needs only Docker (the
image is a self-contained multi-stage build).

The pure packages (`policy`, `node`, `planner`, `actuator`, `reconcile`,
`metrics`, `config`) have **no Kubernetes dependency** and unit-test offline;
`kube`/`cmd` are the thin client-go integration layer.

```sh
make check    # gofmt + vet + test + build
make docker   # build the container image
```

To validate the full execute path (label → agent eviction → controller
actuation) on a local [kind](https://kind.sigs.k8s.io) cluster — the same check
CI runs:

```sh
kind create cluster --name hs-e2e --config e2e/kind.yaml --wait 120s
KIND_CLUSTER=hs-e2e bash e2e/run.sh
```

Release images and charts are cosign-signed — see "Verifying release artifacts"
in the [README](README.md#verifying-release-artifacts).

## License of contributions

By opening an issue with a suggestion, you agree that any resulting change may be
released under the project's Apache-2.0 license.
