# Database schema

Generated from the V1 repository contract.

- `capsules`: immutable manifests and searchable identity fields
- `payloads`: content-addressed blob metadata and reference counts
- `capsule_payloads`: manifest-to-blob references
- `quarantine_records`: policy-safe ingest diagnostics
- `tokens`: hashed scoped bearer tokens
- `jobs` and `job_events`: durable leased work and SSE history
- `evaluators` and `evaluator_runs`: versioned incident gates and their evidence
- `audit_events`: payload-free administrative and retention evidence
