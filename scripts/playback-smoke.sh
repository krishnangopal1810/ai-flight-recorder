#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/playback-smoke.sh <artifact-directory>'
  echo 'Damages two policy-safe hosted payloads and proves API, completeness, and inspector playback remain passive and partially available.'
  exit 0
fi
if [[ $# -ne 1 ]]; then
  echo 'playback-smoke: expected one artifact directory; run scripts/playback-smoke.sh --help' >&2
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
assert len(result["seeded"]) == 1, result
print(result["seeded"][0])
PY
)
authorized_curl "$base_url/api/v1/capsules/$capsule_id" >"$artifact_root/verified-before-damage.json"
digests=()
while IFS= read -r digest; do
  digests+=("$digest")
done < <(python3 - "$artifact_root/verified-before-damage.json" <<'PY'
import json, sys
capsule = json.load(open(sys.argv[1]))
digests = capsule["integrity"]["payload_digests"]
assert len(digests) >= 3, capsule
print(digests[0].removeprefix("sha256:"))
print(digests[1].removeprefix("sha256:"))
PY
)
missing_hex="${digests[0]}"
corrupt_hex="${digests[1]}"
docker compose -f deploy/docker-compose.yml run --rm --no-deps --entrypoint /bin/sh minio-init -c '
  mc alias set local http://minio:9000 flight flight-dev-only-password >/dev/null
  mc rm "local/flight/blobs/sha256/$1" >/dev/null
  printf damaged-playback-evidence | mc pipe "local/flight/blobs/sha256/$2" >/dev/null
' playback-damage "$missing_hex" "$corrupt_hex" >"$artifact_root/storage-damage.log" 2>&1

strict_status=$(curl --silent --show-error --connect-timeout 3 --max-time 30 -o "$artifact_root/strict-error.json" -w '%{http_code}' -H "Authorization: Bearer ${token}" "$base_url/api/v1/capsules/$capsule_id")
if [[ "$strict_status" != "422" ]]; then
  echo "playback-smoke: strict capsule verification returned ${strict_status}, expected 422 incomplete_capsule" >&2
  exit 1
fi
payload_status=$(curl --silent --show-error --connect-timeout 3 --max-time 30 -o "$artifact_root/corrupt-payload-error.json" -w '%{http_code}' -H "Authorization: Bearer ${token}" "$base_url/api/v1/capsules/$capsule_id/payloads/sha256:$corrupt_hex")
if [[ "$payload_status" != "500" ]]; then
  echo "playback-smoke: corrupt payload endpoint returned ${payload_status}, expected 500 storage" >&2
  exit 1
fi
python3 - "$artifact_root/strict-error.json" "$artifact_root/corrupt-payload-error.json" <<'PY'
import json, sys
strict, corrupt = (json.load(open(path))["error"] for path in sys.argv[1:])
assert strict["code"] == "incomplete_capsule", strict
assert corrupt["code"] == "storage", corrupt
assert "damaged-playback-evidence" not in json.dumps(corrupt), corrupt
PY
authorized_curl "$base_url/api/v1/capsules/$capsule_id/playback" >"$artifact_root/playback.json"
authorized_curl "$base_url/api/v1/capsules/$capsule_id/completeness" >"$artifact_root/completeness.json"
python3 - "$artifact_root/playback.json" "$artifact_root/completeness.json" "$capsule_id" <<'PY'
import json, sys
playback, completeness = (json.load(open(path)) for path in sys.argv[1:3])
capsule_id = sys.argv[3]
assert playback["capsule"]["capsule_id"] == capsule_id, playback
assert playback["manifest_verified"] is True and playback["executed"] is False, playback
assert playback["application_processes_started"] == 0 and playback["outbound_requests"] == 0, playback
statuses = {payload["availability"] for payload in playback["payloads"]}
assert {"available", "missing", "corrupt"} <= statuses, statuses
codes = {warning["code"] for warning in playback["warnings"]}
assert {"payload_missing", "payload_corrupt"} <= codes, playback["warnings"]
assert "damaged-playback-evidence" not in json.dumps(playback), "corrupt bytes were rendered"
assert playback["completeness"] == completeness, (playback["completeness"], completeness)
assert completeness["playback"]["state"] == "incomplete", completeness
assert completeness["diff"]["state"] == "incomplete", completeness
assert completeness["replay"]["state"] == "blocked", completeness
assert completeness["bisect"]["state"] == "blocked", completeness
PY

(cd web && \
  PLAYWRIGHT_BASE_URL="$base_url" \
  FLIGHT_PLAYBACK_E2E=1 \
  FLIGHT_PLAYBACK_CAPSULE_ID="$capsule_id" \
  FLIGHT_PLAYWRIGHT_OUTPUT_DIR="$artifact_root/playwright" \
  npx playwright test tests/playback-live.spec.ts) >"$artifact_root/inspector.log" 2>&1 || {
  tail -100 "$artifact_root/inspector.log" >&2 || true
  exit 1
}

printf '{"status":"passed","capsule_id":"%s","strict_status":%s,"corrupt_payload_status":%s,"application_processes_started":0,"outbound_requests":0}\n' "$capsule_id" "$strict_status" "$payload_status" >"$artifact_root/status.json"
echo "playback-smoke: passed; retained evidence: $artifact_root"
