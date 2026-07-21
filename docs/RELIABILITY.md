# Reliability

- Ingestion acknowledges only committed metadata and payload references.
- Collector backpressure is bounded and reports partial acceptance.
- Playback verifies the immutable manifest without requiring blob availability, then verifies every payload independently. Missing, corrupt, or temporarily unavailable bytes degrade only their references and dynamic completeness; the verified logical tree and unaffected evidence remain inspectable.
- Playback remains available when replay workers and providers are unavailable and cannot invoke application entrypoints or outbound transports.
- Job leases have heartbeats and safe expiry recovery.
- Retention rolls back expiry when reference accounting is uncertain, serializes blob cleanup against same-digest writes, and alerts while retaining data on cleanup failure.
- Every error has a stable code, safe message, and remediation.
