# Releasing

Releases are tag-driven and require human approval for product or security-sensitive changes.

1. Complete the active execution plan, update generated references and the quality score, and run `make verify` from a clean worktree.
2. Review migration, compatibility, replay-isolation, privacy, secret-history, dependency, and license evidence. Confirm the technical-debt tracker has owners and targets for every deferral.
3. For the public alpha, complete the [reader study](launch/reader-study.md) and [publication audit](launch/publication-audit.md). Obtain explicit owner confirmation before changing visibility or creating the first public tag.
4. Merge the reviewed PR, confirm the commit is the tip of `main`, then create an annotated `vX.Y.Z` tag. Beta and stable tags remain blocked until `docs/beta-gates.json` contains reviewed passing evidence for every PRD metric.
5. The tag workflow repeats every repository gate, scans Git history, audits dependencies and licenses, then builds Linux/macOS amd64/arm64 CLI archives, a Python wheel/source distribution, and a TypeScript tarball. Versions are derived from the tag, installed in clean smoke environments, and recorded in `release-manifest.json`.
6. CLI and SDK assets receive SBOMs and signed checksum inventories. Verify both `checksums.sigstore.json` and `release-checksums.sigstore.json`; never install an asset whose digest is absent from the manifest.
7. The workflow publishes multi-architecture service, Docker-demo, and real refund-agent images with provenance and SBOM attestations. All three images are signed by digest.
8. The workflow pulls the published digests, runs the complete Docker-only refund investigation, verifies `deny → approve` at `refund-decision → model request`, and attaches image identities plus demo output to the GitHub release.
9. Verify the release signature bundles and image signatures independently, record the evidence in the completed execution plan, then complete the public repository settings checklist.

Do not reuse tags or publish mutable deployment references as evidence. Production manifests should pin the service image digest. A failed or partially published release is superseded with a new patch version; release assets are not silently replaced.
