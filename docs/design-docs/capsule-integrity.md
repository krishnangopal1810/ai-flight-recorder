# Capsule integrity

## Decision

Payload identity is SHA-256 over exact post-policy bytes. Manifests are serialized as deterministic JSON. A capsule root digest covers the manifest digest and sorted payload digests. The root digest is verified before playback, replay, diff, or import.

## Failure behavior

Missing and corrupt blobs remain inspectable as unavailable evidence, but make strict replay incomplete. Import is staged and becomes visible only after all supported schema and digest checks pass.
