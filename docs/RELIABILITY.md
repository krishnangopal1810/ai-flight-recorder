# Reliability

- Ingestion acknowledges only committed metadata and payload references.
- Collector backpressure is bounded and reports partial acceptance.
- Playback remains available when replay workers and providers are unavailable.
- Job leases have heartbeats and safe expiry recovery.
- Retention favors preservation when reference accounting is uncertain.
- Every error has a stable code, safe message, and remediation.
