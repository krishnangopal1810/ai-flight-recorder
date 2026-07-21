# Reliability

- Ingestion acknowledges only committed metadata and payload references.
- Collector backpressure is bounded and reports partial acceptance.
- Playback remains available when replay workers and providers are unavailable.
- Job leases have heartbeats and safe expiry recovery.
- Retention rolls back expiry when reference accounting is uncertain, serializes blob cleanup against same-digest writes, and alerts while retaining data on cleanup failure.
- Every error has a stable code, safe message, and remediation.
