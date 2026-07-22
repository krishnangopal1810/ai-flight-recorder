# Public alpha publication audit

No item in this file authorizes publication. Present the completed evidence to the repository owner and obtain explicit confirmation immediately before changing visibility or creating `v0.1.0-alpha.1`.

## Blocking evidence

- [ ] `main` is clean, pushed, and green at the exact release commit.
- [x] `make verify` passes with PostgreSQL, S3-compatible storage, and mandatory OCI security probes.
- [x] The full Git history secret scan passes with only exact documented fixtures allowlisted.
- [x] Go, Python, and JavaScript vulnerability audits pass, with the unlinked no-fix Go module residual documented in the alpha audit report.
- [x] Dependency license review passes for shipped CLI, service, launcher, agent, SDK, and inspector artifacts.
- [ ] macOS arm64 and Linux amd64 Docker-only journeys finish within five minutes after image pull.
- [x] Repeated `start`, `status`, `stop`, and `stop --purge` prove labelled resource isolation and safe retention on the local macOS arm64 engine.
- [x] The recorded retriever and eligibility callbacks remain offline during replay.
- [ ] The five-reader study meets every threshold in [reader-study.md](reader-study.md).
- [ ] README anchors, links, alt text, 8–12 second GIF, static fallback, mobile layout, and GitHub rendering pass.
- [ ] Service, demo, and agent images publish by digest with SBOM, provenance, and keyless signatures.
- [ ] CLI archives, checksums, archive SBOMs, and signed checksum bundle verify from a clean machine.

## Repository settings after confirmation

Description:

> Reproduce production AI agent failures. Freeze recorded dependencies, replay one change safely, and find the first meaningful divergence.

Topics: `ai-agents`, `agent-debugging`, `developer-tools`, `llmops`, `opentelemetry`, `observability`, `replay`, `testing`.

- [ ] Upload `docs/assets/brand/social-preview.png` as the repository social preview.
- [ ] Enable Discussions and create **Welcome**, **Show and Tell**, **Incident Stories**, and **Roadmap** categories.
- [ ] Enable private vulnerability reporting and dependency security updates.
- [ ] Require the `verify` check and pull-request review on `main`; block force pushes and branch deletion.
- [ ] Set service, demo, and agent package visibility to public only after the signed release succeeds.
- [ ] Create the four roadmap-backed `good first issue` items with acceptance criteria and labels.
- [ ] Verify issue forms, contribution guide, conduct policy, root security policy, roadmap, and Discussions links from a signed-out session.

## Owner confirmation

Owner: Pending
Release commit: Pending
Audit evidence URL: Pending
Confirmation timestamp: Pending
