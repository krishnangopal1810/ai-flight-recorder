# Operations, backup, and recovery

## Worktree-local development

`make dev-up` derives the Compose project, ports, network, volumes, and database namespace from the absolute worktree path. This lets multiple worktrees boot independently. Use `./scripts/dev-stack.sh ps` to see mapped ports, `make observe QUERY=logs|metrics|traces|jobs` for structured evidence, and `make dev-down` for teardown. Browser artifacts remain under ignored worktree-local test directories.

The Compose stack is for local development. Its fixed bootstrap token and credentials are not production secrets, every published port is loopback-only, and `FLIGHT_BEHIND_TLS_PROXY=true` models a trusted TLS-terminating proxy.

## Single-tenant hosted service

Set PostgreSQL and S3-compatible storage through `FLIGHT_DATABASE_URL`, `FLIGHT_S3_ENDPOINT`, `FLIGHT_S3_BUCKET`, `FLIGHT_S3_ACCESS_KEY`, and `FLIGHT_S3_SECRET_KEY`. Disable local bypass, configure native `FLIGHT_TLS_CERT`/`FLIGHT_TLS_KEY` or a trusted TLS reverse proxy, and provide a random 256-bit bootstrap token. Replay credentials use `FLIGHT_REPLAY_CREDENTIAL_<NAME>` and are released only when `flight.yaml` declares the exact environment name.

The coordinator needs access to the selected Docker or Podman engine. Replayed application containers never receive that socket; they run non-root, capability-free, read-only except for tmpfs/output, resource-limited, and on an internal network whose only peer is the Flight gateway.

## Health and diagnosis

- `GET /healthz` proves the HTTP process is responsive.
- `GET /metrics` exposes payload-free counters.
- `flight capsule list --json` verifies authenticated repository reads.
- `flight job <id> --json` and `/api/v1/jobs/<id>/events` expose durable progress and remediation-oriented errors.
- Logs are one JSON object per line and contain identifiers, states, durations, and error categories—never capsule payloads.

## Backup

Create storage-consistent backups during a write pause or from coordinated database/object-store snapshots:

1. Stop ingestion and replay workers, then wait for active jobs to reach a terminal state.
2. Snapshot PostgreSQL with `pg_dump --format=custom` or the provider's point-in-time facility.
3. Snapshot/version the entire configured S3 bucket, retaining object versions that overlap the database recovery point.
4. Record the application version, schema version, database recovery point, object-store version, and SHA-256 checksums in the backup manifest.
5. Resume traffic only after both snapshots and the manifest are durable.

SQLite deployments back up the database with SQLite's online backup API or `VACUUM INTO` and copy the blob tree at the same quiesced point. Copying only `flight.db` or only `blobs/` is not a valid backup.

## Restore and upgrade

Restore into an empty deployment: restore objects first, restore the database second, start one service instance to apply forward-only idempotent migrations, and verify several capsules with `flight capsule export` followed by an offline import into a temporary local store. Keep the old deployment read-only until verification completes.

Before upgrading, take a coordinated backup and run `make verify` against the target version. Schema major versions are rejected atomically; unknown minor fields survive round trips. Rollback after a migration means restoring the coordinated pre-upgrade database and object snapshots rather than running destructive down-migrations.

Failure injection should cover worker termination after object write/before metadata commit, expired job leases, unavailable object storage, corrupt blobs, partial bundle import, and gateway termination. Conservative cleanup leaves unreferenced objects for later garbage collection rather than risking referenced evidence.
