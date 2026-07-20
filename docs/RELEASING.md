# Releasing

Releases are tag-driven and require human approval for product or security-sensitive changes.

1. Complete the active execution plan, update generated references and the quality score, and run `make verify` from a clean worktree.
2. Review migration, compatibility, replay-isolation, and privacy evidence. Confirm the technical-debt tracker has owners and targets for every deferral. Beta and stable tags are blocked until `docs/beta-gates.json` contains reviewed evidence that passes every PRD metric; alpha tags remain available for collecting that evidence.
3. Merge the reviewed PR, then create an annotated `vX.Y.Z` tag on `main`.
4. The release workflow builds Linux/macOS amd64/arm64 CLI archives, checksums and SBOMs, signs the checksum provenance keylessly, and publishes a multi-architecture Linux service image to GHCR with a keyless signature.
5. Verify the GitHub release signature bundle, pull the service image by digest, smoke-test import/playback, and record the release evidence in the completed execution plan.

Do not reuse tags or publish mutable deployment references as evidence. Production manifests should pin the service image digest. A failed or partially published release is superseded with a new patch version; release assets are not silently replaced.
