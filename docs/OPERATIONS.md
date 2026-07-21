# Operations, backup, and recovery

## Worktree-local development

`make dev-up` derives the Compose project, ports, network, volumes, database namespace, and artifact root from the absolute worktree path. It builds in the background and returns only after the service is healthy. Use `./scripts/worktree-env.sh --json` and `./scripts/dev-stack.sh ps` to inspect the mapping. Query evidence with `make observe QUERY=logs`, `metrics`, or `capsules`; pass an ID to `./scripts/observe.sh traces <capsule-id>`, `jobs <job-id>`, or `job-events <job-id>`. `make dev-down` removes only that worktree's containers, network, and volumes. Browser artifacts remain under the ignored worktree-local `.artifacts/` directory.

`make harness-check` creates a second temporary Git worktree containing the current source state, proves that its identifiers do not collide with the caller, bootstraps dependencies, runs tests, starts and seeds the complete stack, exercises metrics/capsule/trace queries plus CLI playback and diff, runs real Python and TypeScript reference-agent replays, runs the inspector against live data, and tears everything down. The replay journeys record standard OTLP, compare equivalent cross-language logical capsules, execute both registered JSON-stdin entrypoints under OCI isolation, verify exact substituted candidates, consume terminal job evidence, and prove unmatched or incomplete metadata fails without an undeclared live fallback. Every run retains `status.json` with a failure classification, exact reproduction instructions, step logs, replay plans/jobs/capsules/diffs, safe SDK diagnostics, Compose diagnostics, DOM attachments, traces, screenshots, and video under `.artifacts/harness/`. Registry-dependent image builds are bounded so a stalled pull produces actionable evidence instead of hanging an agent indefinitely.

The Compose stack is for local development. Its fixed bootstrap token and credentials are not production secrets, every published port is loopback-only, and `FLIGHT_INSECURE_DEVELOPMENT=true` makes the otherwise-invalid cleartext container listeners explicit. The API and both OTLP transports still require the scoped bootstrap token. Never use this override on a shared network.

## Single-tenant hosted service

Set PostgreSQL and S3-compatible storage through `FLIGHT_DATABASE_URL`, `FLIGHT_S3_ENDPOINT`, `FLIGHT_S3_BUCKET`, `FLIGHT_S3_ACCESS_KEY`, and `FLIGHT_S3_SECRET_KEY`. Disable local bypass and provide a random 256-bit bootstrap token (`frt_` plus 32 unpadded base64url bytes). For native TLS, set `FLIGHT_TLS_CERT` and `FLIGHT_TLS_KEY`; the identity protects the API, OTLP/HTTP, and OTLP/gRPC listeners. For proxy termination, set `FLIGHT_BEHIND_TLS_PROXY=true` and `FLIGHT_TRUSTED_PROXY_CIDRS` to the proxy source networks. The HTTP proxy must overwrite `X-Forwarded-Proto` with `https`; the HTTP and gRPC upstreams must originate from those networks, and the raw upstream ports must not be publicly routable. Health is unauthenticated but transport-checked; metrics require `admin`. Replay credentials use `FLIGHT_REPLAY_CREDENTIAL_<NAME>` and are released only when `flight.yaml` declares the exact environment name.

Startup fails when any non-loopback listener lacks explicit authentication and native or trusted-proxy transport. `FLIGHT_INSECURE_DEVELOPMENT=true` is the sole override and emits a structured warning. Local SQLite files and blobs are restricted to the service account; hosted encryption at rest is supplied and enforced by the configured PostgreSQL and S3-compatible services.

`flight --url <https-url> --token <token> record -- <command>` supplies the selected token to Flight SDKs and to standard OTLP exporters through a URL-encoded `Authorization` header. For direct OTLP/gRPC producers, send the same `authorization: Bearer <token>` metadata and trust the native or proxy certificate chain.

## Retention and payload limits

`policy.default_retention` sets the capsule lifetime and `policy.project_retention` overrides it by exact project ID; a zero duration is an explicit indefinite hold. The service evaluates expiry immediately at startup and every `server.retention_interval` (default `1h`, or `FLIGHT_RETENTION_INTERVAL`). Expiry removes the manifest and decrements its references transactionally. Unreferenced blobs are then removed conservatively; a failed accounting check or blob operation retains the affected data and emits `retention evaluation failed` with a safe error code and affected capsule count. Successful deletions create one `retention_delete` audit event containing only capsule, reference, and per-project counts.

`policy.max_field_size` remains the default omit limit. Ordered `policy.payload_limits` can override operation input, operation output, tool-definition, or artifact payloads; the first exact field/class match wins. `keep` is an explicit exemption and has no `max_bytes`; `omit` records a typed omission; `truncate` requires at least 64 bytes, stores a bounded policy-safe prefix (a valid JSON marker for JSON payloads), records `original_size` plus `payload_truncated`, and marks affected playback/diff incomplete and replay/bisect blocked where exact evidence is required. Artifact rules match the exact declared `artifact_class`.

```yaml
server:
  retention_interval: 15m
policy:
  default_retention: 720h
  project_retention:
    incident-hot: 168h
    legal-hold: 0s
  payload_limits:
    - {name: omit-large-tools, field: model_configuration.tool_definitions, max_bytes: 262144, action: omit}
    - {name: truncate-transcripts, field: artifacts, artifact_class: transcript, max_bytes: 1048576, action: truncate}
    - {name: keep-results, field: operations.output, action: keep}
```

The coordinator needs access to the selected Docker or Podman engine. Replayed application containers never receive that socket; they run non-root, capability-free, read-only except for declared tmpfs scratch space, resource-limited, and on an internal network whose only peer is the Flight gateway. The operator selects the trusted gateway with `FLIGHT_GATEWAY_IMAGE`; replay API clients cannot replace it. Set `FLIGHT_OCI_EGRESS_NETWORK` to an existing engine network where declared model and sandbox hosts resolve. The gateway joins that network only when the immutable plan has live allowed hosts; an all-substituted replay remains on its private internal network.

## Playback and damaged evidence

`flight playback <capsule-id> --json` and `GET /api/v1/capsules/{id}/playback` are passive: they do not launch the recorded entrypoint, a replay worker, or any provider request. The response verifies the manifest/root identity first and classifies each declared payload independently as `available`, `missing`, `corrupt`, or `unavailable`. Available values have bounded, encoding-labelled previews; corrupt bytes are never returned. The response carries dynamic completeness, so payload damage leaves unaffected playback evidence inspectable while marking playback/diff incomplete and replay/bisect blocked.

Treat `payload_missing` as a storage/retention recovery incident, `payload_corrupt` as an integrity incident, and `payload_unavailable` as a backend availability incident. Restore the original digest-addressed object from a coordinated backup, then retry strict capsule verification. Do not repair an object under an existing digest with different bytes.

Compose mounts `/var/run/docker.sock` by default because bind sources are resolved on the engine host (including a Colima VM), not at the local client context path. Set `FLIGHT_DOCKER_SOCKET` only when a rootless or remote daemon exposes a different path on that daemon host.

Replay does not bind client-local temporary paths into containers. The immutable replay envelope is copied through the OCI API into a daemon-local named volume and mounted read-only; captured candidate OTLP is copied back through the OCI API from a bounded gateway tmpfs. This is required for Colima, remote engines, and the service-inside-Compose topology. `make security` builds the trusted local gateway image and runs this contract uncached when Docker is available.

## Health and diagnosis

- `GET /healthz` proves the HTTP process is responsive.
- Authenticated `GET /metrics` with `admin` scope exposes payload-free counters.
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

Before upgrading, take a coordinated backup and run `make verify` against the target version. Numeric schema `1.x` manifests are accepted and retain unknown fields at every document depth; unsupported majors are rejected before any repository metadata or blob write. A compatible-minor bundle can be re-exported byte-for-byte and played back offline without changing capsule identity. Rollback after a migration means restoring the coordinated pre-upgrade database and object snapshots rather than running destructive down-migrations.

Failure injection should cover worker termination after object write/before metadata commit, expired job leases, unavailable object storage, corrupt blobs, partial bundle import, and gateway termination. Conservative cleanup leaves unreferenced objects for later garbage collection rather than risking referenced evidence.
