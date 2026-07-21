#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/lib/worktree.sh"
flight_worktree_init
cd "$FLIGHT_REPO_ROOT"

action="${1:-seed}"
artifact_root="${FLIGHT_ARTIFACT_ROOT}/investigation-demo"
token='frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
base_url="http://127.0.0.1:${FLIGHT_HTTP_PORT}"
image_tag="flight-investigation-demo-${FLIGHT_WORKTREE_ID}:local"
compose_network="${COMPOSE_PROJECT_NAME}_default"

authorized_curl() {
  curl --fail --silent --show-error --connect-timeout 3 --max-time 30 \
    -H "Authorization: Bearer ${token}" "$@"
}

case "$action" in
  cleanup)
    docker image rm "$image_tag" >/dev/null 2>&1 || true
    exit 0
    ;;
  --help|-h|help)
    echo 'usage: scripts/investigation-demo.sh <seed|cleanup>'
    exit 0
    ;;
  seed) ;;
  *) echo 'investigation-demo: expected seed or cleanup' >&2; exit 2 ;;
esac

mkdir -p "$artifact_root"

if [[ -s "$artifact_root/summary.json" ]] && python3 - "$artifact_root/summary.json" "$base_url" "$token" <<'PY'
import json, sys, urllib.error, urllib.request
summary = json.load(open(sys.argv[1]))
if summary.get("project_id") != "refund-operations":
    raise SystemExit(1)
try:
    for key in ("source_run_id", "candidate_run_id"):
        request = urllib.request.Request(
            f"{sys.argv[2]}/api/v1/capsules/{summary[key]}",
            headers={"Authorization": f"Bearer {sys.argv[3]}"},
        )
        with urllib.request.urlopen(request, timeout=3) as response:
            if response.status != 200:
                raise SystemExit(1)
except (KeyError, OSError, urllib.error.HTTPError, urllib.error.URLError):
    raise SystemExit(1) from None
PY
then
  echo "investigation-demo: existing real-agent investigation is ready"
  exit 0
fi

"$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 240 -- \
  docker build --tag "$image_tag" --file examples/investigation-agent/Dockerfile . \
  >"$artifact_root/image-build.log" 2>&1
image_id=$(docker image inspect --format '{{.Id}}' "$image_tag")
if [[ ! "$image_id" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "investigation-demo: OCI runtime returned a non-immutable image ID: $image_id" >&2
  exit 1
fi
docker network inspect "$compose_network" >"$artifact_root/network.json"

authorized_curl "$base_url/api/v1/capsules?project=refund-operations&limit=500" >"$artifact_root/runs-before.json"
printf '%s' '{"request_id":"refund-1842","days_since_purchase":12,"amount_usd":79}' | \
  "$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 60 -- \
  docker run --rm -i --network "$compose_network" \
    -e OTEL_EXPORTER_OTLP_ENDPOINT=http://flight:4318 \
    -e "FLIGHT_OTLP_TOKEN=$token" \
    -e "FLIGHT_REPLAY_IMAGE=$image_id" \
    "$image_tag" >"$artifact_root/source-output.json" 2>"$artifact_root/source-stderr.log"

deadline=$((SECONDS + 30))
while true; do
  authorized_curl "$base_url/api/v1/capsules?project=refund-operations&limit=500" >"$artifact_root/runs-after.json"
  if source_id=$(python3 - "$artifact_root/runs-before.json" "$artifact_root/runs-after.json" <<'PY'
import json, sys
before = {item["capsule_id"] for item in (json.load(open(sys.argv[1])).get("items") or [])}
after = [item for item in (json.load(open(sys.argv[2])).get("items") or [])
         if item["capsule_id"] not in before and item["service_name"] == "refund-review-agent"]
if len(after) != 1 or after[0]["terminal_status"] != "error":
    raise SystemExit(1)
print(after[0]["capsule_id"])
PY
  ); then
    break
  fi
  if (( SECONDS >= deadline )); then
    echo 'investigation-demo: failed baseline was not finalized within 30 seconds' >&2
    exit 1
  fi
  sleep 1
done

python3 - "$source_id" >"$artifact_root/replay-request.json" <<'PY'
import json, sys
print(json.dumps({
    "source_capsule_id": sys.argv[1],
    "model": "policy-grounded",
    "substitute_llm": False,
    "allow_sandboxed_side_effects": False,
}, separators=(",", ":")))
PY
authorized_curl -H 'Content-Type: application/json' \
  --data-binary @"$artifact_root/replay-request.json" \
  "$base_url/api/v1/replays" >"$artifact_root/job-created.json"
job_id=$(python3 - "$artifact_root/job-created.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["id"])
PY
)

deadline=$((SECONDS + 180))
while true; do
  authorized_curl "$base_url/api/v1/jobs/$job_id" >"$artifact_root/job.json"
  state=$(python3 - "$artifact_root/job.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["state"])
PY
)
  case "$state" in
    completed) break ;;
    failed|blocked|inconclusive)
      echo "investigation-demo: controlled replay ended in $state" >&2
      exit 1
      ;;
  esac
  if (( SECONDS >= deadline )); then
    echo 'investigation-demo: controlled replay did not finish within 180 seconds' >&2
    exit 1
  fi
  sleep 1
done

candidate_id=$(python3 - "$artifact_root/job.json" <<'PY'
import json, sys
job = json.load(open(sys.argv[1]))
result = job["result"]
if result["outcome"] != "divergent" or not result["candidate_capsule_id"]:
    raise SystemExit(f"expected a divergent candidate, got {result}")
print(result["candidate_capsule_id"])
PY
)
authorized_curl -H 'Content-Type: application/json' \
  --data "{\"baseline_capsule_id\":\"$source_id\",\"candidate_capsule_id\":\"$candidate_id\"}" \
  "$base_url/api/v1/diffs" >"$artifact_root/diff.json"
authorized_curl "$base_url/api/v1/jobs/$job_id/events" >"$artifact_root/job-events.sse"

python3 - "$source_id" "$job_id" "$candidate_id" "$image_id" >"$artifact_root/summary.json" <<'PY'
import json, sys
print(json.dumps({
    "scenario": "eligible refund denied by an overly conservative agent prompt",
    "project_id": "refund-operations",
    "source_run_id": sys.argv[1],
    "replay_job_id": sys.argv[2],
    "candidate_run_id": sys.argv[3],
    "candidate_change": "policy-grounded",
    "image_digest": sys.argv[4],
}, indent=2))
PY
echo "investigation-demo: real-agent investigation ready at /runs/$source_id/understand"
