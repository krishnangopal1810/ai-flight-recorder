#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/privacy-smoke.sh <artifact-directory>'
  echo 'Seeds sensitive OTLP evidence and scans hosted storage, bundles, quarantine, logs, metrics, and diagnostics.'
  exit 0
fi
if [[ $# -ne 1 ]]; then
  echo 'privacy-smoke: expected one artifact directory; run scripts/privacy-smoke.sh --help' >&2
  exit 2
fi

source "$(dirname "$0")/lib/worktree.sh"
flight_worktree_init
cd "$FLIGHT_REPO_ROOT"

artifact_root="$1"
mkdir -p "$artifact_root"
artifact_root=$(cd "$artifact_root" && pwd)
token='frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
base_url="http://127.0.0.1:${FLIGHT_HTTP_PORT}"

authorized_curl() {
  curl --fail --silent --show-error --connect-timeout 3 --max-time 30 \
    -H "Authorization: Bearer ${token}" "$@"
}

go run ./cmd/flight --url "$base_url" --token "$token" --json demo seed-sensitive >"$artifact_root/seed.json" 2>"$artifact_root/seed-stderr.log"
capsule_id=$(python3 - "$artifact_root/seed.json" <<'PY'
import json, sys
result = json.load(open(sys.argv[1]))
ids = result["seeded"]
assert len(ids) == 1 and result["quarantine_code"] == "validation", result
assert result["redactions"]["authorization"] > 0 and result["redactions"]["email"] > 0, result
print(ids[0])
PY
)

authorized_curl "$base_url/api/v1/capsules/$capsule_id" >"$artifact_root/capsule.json" 2>"$artifact_root/capsule-curl-stderr.log"
go run ./cmd/flight --url "$base_url" --token "$token" capsule export "$capsule_id" "$artifact_root/capsule.tar.zst" >"$artifact_root/export.json" 2>"$artifact_root/export-stderr.log"
mkdir -p "$artifact_root/bundle" "$artifact_root/object-store"
tar -xf "$artifact_root/capsule.tar.zst" -C "$artifact_root/bundle"
./scripts/observe.sh metrics >"$artifact_root/metrics.txt" 2>"$artifact_root/metrics-stderr.log"
./scripts/dev-stack.sh logs 500 >"$artifact_root/logs.txt" 2>"$artifact_root/logs-stderr.log"
docker compose -f deploy/docker-compose.yml exec -T postgres \
  pg_dump --data-only --column-inserts -U flight flight >"$artifact_root/postgres.sql" 2>"$artifact_root/postgres-stderr.log"
docker compose -f deploy/docker-compose.yml cp minio:/data/. "$artifact_root/object-store" >"$artifact_root/object-copy.log" 2>&1

python3 - "$artifact_root" "$capsule_id" <<'PY'
import json, pathlib, re, sys

root, capsule_id = pathlib.Path(sys.argv[1]), sys.argv[2]
seeds = [
    b"AFR_TEST_API_KEY_7F3A91",
    b"AFR_TEST_TOKEN_9C2B84",
    b"AFR_TEST_PASSWORD_4D8E17",
    b"alice.privacy@example.test",
]
for path in root.rglob("*"):
    if not path.is_file():
        continue
    data = path.read_bytes()
    for seed in seeds:
        if seed in data:
            raise SystemExit(f"privacy-smoke: seeded value {seed!r} reached {path}")

capsule = json.loads((root / "capsule.json").read_text())
assert capsule["capsule_id"] == capsule_id, capsule
privacy = capsule["privacy"]
assert privacy["mode"] == "strict", privacy
assert privacy["redactions"]["authorization"] > 0, privacy
assert privacy["redactions"]["email"] > 0, privacy
assert (root / "bundle" / "manifest.json").is_file(), "bundle manifest missing"
assert any((root / "bundle" / "blobs" / "sha256").iterdir()), "bundle payloads missing"

database = (root / "postgres.sql").read_text()
assert "root_trace_missing" in database, "policy-safe quarantine diagnostic missing"
metrics = (root / "metrics.txt").read_text()
accepted = re.search(r"flight_otlp_accepted_spans_total (\d+)", metrics)
rejected = re.search(r"flight_otlp_rejected_spans_total (\d+)", metrics)
assert accepted and int(accepted.group(1)) >= 2, metrics
assert rejected and int(rejected.group(1)) >= 2, metrics
assert any(path.is_file() for path in (root / "object-store").rglob("*")), "object-store evidence missing"
PY

printf '{"status":"passed","capsule_id":"%s","reproduce":"scripts/privacy-smoke.sh <artifact-directory>"}\n' "$capsule_id" >"$artifact_root/status.json"
echo "privacy-smoke: passed; retained evidence: $artifact_root"
