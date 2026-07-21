#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/completeness-smoke.sh <artifact-directory>'
  echo 'Seeds one missing tool response and proves the API, CLI, preflight, and inspector completeness contracts.'
  exit 0
fi
if [[ $# -ne 1 ]]; then
  echo 'completeness-smoke: expected one artifact directory; run scripts/completeness-smoke.sh --help' >&2
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

go run ./cmd/flight --url "$base_url" --token "$token" --json demo seed-incomplete >"$artifact_root/seed.json"
capsule_id=$(python3 - "$artifact_root/seed.json" <<'PY'
import json, sys
ids = json.load(open(sys.argv[1]))["seeded"]
if len(ids) != 1:
    raise SystemExit(f"completeness-smoke: expected one seeded capsule, got {ids}")
print(ids[0])
PY
)

authorized_curl "$base_url/api/v1/capsules/$capsule_id/completeness" >"$artifact_root/api.json"
go run ./cmd/flight --url "$base_url" --token "$token" --json capsule completeness "$capsule_id" >"$artifact_root/cli.json"
authorized_curl "$base_url/api/v1/capsules/$capsule_id/playback" >"$artifact_root/playback.json"

python3 - "$capsule_id" >"$artifact_root/replay-request.json" <<'PY'
import json, sys
print(json.dumps({
    "source_capsule_id": sys.argv[1],
    "substitute_llm": True,
    "allow_sandboxed_side_effects": False,
}, separators=(",", ":")))
PY
replay_status=$(curl --silent --show-error --connect-timeout 3 --max-time 30 \
  --output "$artifact_root/replay-error.json" --write-out '%{http_code}' \
  -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
  --data-binary @"$artifact_root/replay-request.json" "$base_url/api/v1/replays")

python3 - "$artifact_root/api.json" "$artifact_root/cli.json" "$artifact_root/playback.json" "$artifact_root/replay-error.json" "$capsule_id" "$replay_status" <<'PY'
import json, sys
api, cli, playback, replay_error = (json.load(open(path)) for path in sys.argv[1:5])
capsule_id, replay_status = sys.argv[5:7]
assert api == cli, (api, cli)
assert api["schema_version"] == "1.0" and api["capsule_id"] == capsule_id, api
assert api["playback"] == {"state": "ready"}, api["playback"]
assert api["diff"] == {"state": "ready"}, api["diff"]
for mode in ("replay", "bisect"):
    assert api[mode]["state"] == "blocked", api[mode]
    assert len(api[mode]["missing"]) == 1, api[mode]
    missing = api[mode]["missing"][0]
    assert missing["code"] == "boundary_response_missing", missing
    assert missing["boundary_id"].startswith("boundary_") and missing["boundary_id"].endswith("0302"), missing
assert playback["mode"] == "playback" and playback["executed"] is False, playback
assert playback["capsule"]["capsule_id"] == capsule_id, playback
assert replay_status == "422", replay_status
error = replay_error["error"]
assert error["code"] == "incomplete_capsule", error
assert error["details"]["missing"] == api["replay"]["missing"], error
PY

(
  cd web
  PLAYWRIGHT_BASE_URL="$base_url" \
  FLIGHT_COMPLETENESS_E2E=1 \
  FLIGHT_COMPLETENESS_CAPSULE_ID="$capsule_id" \
  FLIGHT_PLAYWRIGHT_OUTPUT_DIR="$artifact_root/playwright" \
  npx playwright test tests/completeness-live.spec.ts
) >"$artifact_root/inspector.log" 2>&1 || {
  tail -100 "$artifact_root/inspector.log" >&2 || true
  exit 1
}

printf '{"status":"passed","capsule_id":"%s","reproduce":"scripts/completeness-smoke.sh <artifact-directory>"}\n' "$capsule_id" >"$artifact_root/status.json"
echo "completeness-smoke: passed; retained evidence: $artifact_root"
