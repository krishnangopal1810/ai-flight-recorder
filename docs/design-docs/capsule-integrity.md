# Capsule integrity

## Decision

Payload identity is SHA-256 over exact post-policy bytes. Manifests are serialized as deterministic JSON. A capsule root digest covers the manifest digest and sorted payload digests. Finalization rejects captured references absent from that digest tree. Manifest/root identity can therefore be verified independently of blob availability; every referenced blob is then digest-verified independently before its value is used.

An omitted payload reference is manifest evidence, not a stored blob. It records `state`, `reason`, and size, plus a producer-supplied digest only when one is safely available. Collector-side size-limit and serialization omissions are not hashed because capture policy has not approved their bytes. Omission records still affect the manifest and root digest, while only captured post-policy digests appear in the integrity payload tree.

## Failure behavior

Missing and corrupt blobs remain inspectable as typed unavailable evidence, while unaffected manifest facts and verified payloads remain visible. Corrupt bytes are never rendered. Payload damage makes playback and diff incomplete and blocks replay and bisect. Import is staged and becomes visible only after all supported schema and digest checks pass.
