#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/typescript-reference-smoke.sh <artifact-directory> <python-source-capsule>'
  echo 'Proves TypeScript/Python OTLP parity, all-substituted replay, and safe incomplete-metadata diagnostics.'
  exit 0
fi
if [[ $# -ne 2 ]]; then
  echo 'typescript-reference-smoke: expected an artifact directory and Python source capsule' >&2
  exit 2
fi

source "$(dirname "$0")/lib/worktree.sh"
flight_worktree_init
cd "$FLIGHT_REPO_ROOT"

artifact_root="$1"
python_capsule="$2"
mkdir -p "$artifact_root"
artifact_root=$(cd "$artifact_root" && pwd)
python_capsule=$(cd "$(dirname "$python_capsule")" && pwd)/$(basename "$python_capsule")
token='frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
base_url="http://127.0.0.1:${FLIGHT_HTTP_PORT}"
compose_network="${COMPOSE_PROJECT_NAME}_default"
image_tag="flight-reference-typescript-${FLIGHT_WORKTREE_ID}:local"
normal_project='reference-replay-typescript'
diagnostic_project='reference-replay-typescript-diagnostic'

cleanup() {
  local exit_code=$?
  set +e
  docker image rm "$image_tag" >"$artifact_root/image-cleanup.log" 2>&1
  exit "$exit_code"
}
trap cleanup EXIT

authorized_curl() {
  curl --fail --silent --show-error --connect-timeout 3 --max-time 30 \
    -H "Authorization: Bearer ${token}" "$@"
}

new_capsule_id() {
  local before="$1" after="$2" project="$3" deadline
  deadline=$((SECONDS + 30))
  while true; do
    authorized_curl "$base_url/api/v1/capsules?project=$project&limit=100" >"$after"
    if python3 - "$before" "$after" <<'PY'
import json, sys
before = {item["capsule_id"] for item in json.load(open(sys.argv[1])).get("items", [])}
after = [item for item in json.load(open(sys.argv[2])).get("items", []) if item["capsule_id"] not in before]
if len(after) != 1:
    raise SystemExit(1)
print(after[0]["capsule_id"])
PY
    then
      return 0
    fi
    if (( SECONDS >= deadline )); then
      echo "typescript-reference-smoke: capsule for project $project was not finalized within 30s" >&2
      return 1
    fi
    sleep 1
  done
}

wait_for_job() {
  local job_id="$1" output="$2" state deadline
  deadline=$((SECONDS + 120))
  while true; do
    authorized_curl "$base_url/api/v1/jobs/$job_id" >"$output.tmp"
    state=$(python3 - "$output.tmp" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["state"])
PY
)
    case "$state" in
      completed|failed|blocked|inconclusive)
        mv "$output.tmp" "$output"
        return 0
        ;;
    esac
    if (( SECONDS >= deadline )); then
      mv "$output.tmp" "$output"
      echo "typescript-reference-smoke: job $job_id timed out (state=$state)" >&2
      return 1
    fi
    sleep 1
  done
}

authorized_curl "$base_url/api/v1/capsules?project=$normal_project&limit=100" >"$artifact_root/normal-before.json"
authorized_curl "$base_url/api/v1/capsules?project=$diagnostic_project&limit=100" >"$artifact_root/diagnostic-before.json"

"$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 240 -- \
  docker build --tag "$image_tag" --file examples/reference-typescript/Dockerfile . \
  >"$artifact_root/image-build.log" 2>&1
docker image inspect --format '{{.Id}}' "$image_tag" >"$artifact_root/image-id.txt"
image_id=$(tr -d '\n' <"$artifact_root/image-id.txt")
if [[ ! "$image_id" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "typescript-reference-smoke: OCI runtime returned a non-immutable image ID: $image_id" >&2
  exit 1
fi

printf '{"policy_id":"refund"}' | \
  "$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 60 -- \
  docker run --rm -i --network "$compose_network" \
    -e OTEL_EXPORTER_OTLP_ENDPOINT=http://flight:4318 \
    -e "FLIGHT_OTLP_TOKEN=$token" \
    -e "FLIGHT_REFERENCE_IMAGE=$image_id" \
    -e FLIGHT_RECORDING_FIXTURE=1 \
    "$image_tag" >"$artifact_root/normal-stdout.json" 2>"$artifact_root/normal-stderr.log"

source_id=$(new_capsule_id "$artifact_root/normal-before.json" "$artifact_root/normal-after.json" "$normal_project")
printf '%s\n' "$source_id" >"$artifact_root/source-capsule-id.txt"
authorized_curl "$base_url/api/v1/capsules/$source_id" >"$artifact_root/source-capsule.json"
python3 - "$python_capsule" "$artifact_root/source-capsule.json" "$image_id" <<'PY'
import json, sys
python_capsule, typescript_capsule = (json.load(open(path)) for path in sys.argv[1:3])
assert typescript_capsule["project_id"] == "reference-replay-typescript", typescript_capsule
assert typescript_capsule["service_name"] == "typescript-reference-agent", typescript_capsule
assert typescript_capsule["terminal_status"] == "ok", typescript_capsule
assert typescript_capsule["completeness"]["replay"]["state"] == "ready"
entrypoint = typescript_capsule["provenance"]["entrypoint"]
assert entrypoint["image"] == sys.argv[3], entrypoint
assert entrypoint["command"] == ["node", "/app/dist/agent.js"], entrypoint
assert entrypoint["model_hosts"] == ["fake-deps"], entrypoint

def boundaries(capsule):
    return [(item["logical_key"], item["class"], item["parent_logical_key"],
             item["replay_policy"], item["request_digest"], item["response"]["digest"])
            for item in capsule["boundaries"]]

def root(capsule):
    return next(item for item in capsule["operations"] if item["logical_key"] == "agent.answer")

assert boundaries(typescript_capsule) == boundaries(python_capsule), (boundaries(typescript_capsule), boundaries(python_capsule))
assert root(typescript_capsule)["input"]["digest"] == root(python_capsule)["input"]["digest"]
assert root(typescript_capsule)["output"]["digest"] == root(python_capsule)["output"]["digest"]
assert root(typescript_capsule)["attributes"]["flight.reference.model_callback_invoked"] == "true"
PY

python3 - "$source_id" >"$artifact_root/replay-request.json" <<'PY'
import json, sys
print(json.dumps({"source_capsule_id": sys.argv[1], "model": "substitute-only", "substitute_llm": True, "allow_sandboxed_side_effects": False}, separators=(",", ":")))
PY
authorized_curl -H 'Content-Type: application/json' --data-binary @"$artifact_root/replay-request.json" \
  "$base_url/api/v1/replays" >"$artifact_root/replay-created.json"
job_id=$(python3 - "$artifact_root/replay-created.json" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))["id"])
PY
)
wait_for_job "$job_id" "$artifact_root/replay-job.json"
candidate_id=$(python3 - "$artifact_root/replay-job.json" "$source_id" <<'PY'
import json, sys
job = json.load(open(sys.argv[1]))
assert job["state"] == "completed", job
assert job["request"]["allowed_hosts"] == [], job["request"]
result = job["result"]
assert result["source_capsule_id"] == sys.argv[2], result
assert result["substitutions"] == ["api.policy", "llm.answer"], result
assert result.get("live_boundaries", []) == [], result
assert result["outcome"] == "exact" and result["exit_code"] == 0, result
print(result["candidate_capsule_id"])
PY
)
authorized_curl "$base_url/api/v1/capsules/$candidate_id" >"$artifact_root/replay-candidate.json"
python3 - "$artifact_root/source-capsule.json" "$artifact_root/replay-candidate.json" "$source_id" <<'PY'
import json, sys
source, candidate = (json.load(open(path)) for path in sys.argv[1:3])
assert candidate["source_capsule_id"] == sys.argv[3], candidate
assert candidate["terminal_status"] == "ok", candidate
assert [item["response"]["digest"] for item in source["boundaries"]] == [item["response"]["digest"] for item in candidate["boundaries"]]
root = next(item for item in candidate["operations"] if item["logical_key"] == "agent.answer")
assert root["attributes"]["flight.reference.model_callback_invoked"] == "false", root
PY

printf '{"policy_id":"refund"}' | \
  "$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 60 -- \
  docker run --rm -i --network "$compose_network" \
    -e OTEL_EXPORTER_OTLP_ENDPOINT=http://flight:4318 \
    -e "FLIGHT_OTLP_TOKEN=$token" \
    -e "FLIGHT_REFERENCE_IMAGE=$image_id" \
    -e FLIGHT_RECORDING_FIXTURE=1 \
    -e FLIGHT_METADATA_FAILURE_FIXTURE=1 \
    "$image_tag" >"$artifact_root/diagnostic-stdout.json" 2>"$artifact_root/diagnostic-stderr.log"

diagnostic_id=$(new_capsule_id "$artifact_root/diagnostic-before.json" "$artifact_root/diagnostic-after.json" "$diagnostic_project")
printf '%s\n' "$diagnostic_id" >"$artifact_root/diagnostic-capsule-id.txt"
authorized_curl "$base_url/api/v1/capsules/$diagnostic_id" >"$artifact_root/diagnostic-capsule.json"
python3 - "$artifact_root/diagnostic-stdout.json" "$artifact_root/diagnostic-stderr.log" "$artifact_root/diagnostic-capsule.json" <<'PY'
import json, sys
output = json.load(open(sys.argv[1]))
assert output["answer"].startswith("refunds within 30 days;"), output
stderr = open(sys.argv[2]).read()
diagnostics = [json.loads(line) for line in stderr.splitlines() if line.startswith("{")]
assert diagnostics == [{"level": "warning", "code": "flight_sdk_metadata", "operation": "register_entrypoint", "error_type": "TypeError"}], diagnostics
assert "refund" not in stderr and "policy_id" not in stderr, stderr
capsule = json.load(open(sys.argv[3]))
assert capsule["terminal_status"] == "ok", capsule
assert capsule["completeness"]["playback"]["state"] == "ready", capsule["completeness"]
assert capsule["completeness"]["replay"]["state"] == "blocked", capsule["completeness"]
assert capsule["completeness"]["replay"]["missing"] == [{"code": "entrypoint_missing", "message": "a registered replay entrypoint is required"}]
assert capsule["provenance"].get("entrypoint") is None, capsule["provenance"]
PY

http_status=$(curl --silent --show-error --output "$artifact_root/diagnostic-replay-error.json" --write-out '%{http_code}' \
  -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
  --data "{\"source_capsule_id\":\"$diagnostic_id\"}" "$base_url/api/v1/replays")
python3 - "$artifact_root/diagnostic-replay-error.json" "$http_status" <<'PY'
import json, sys
error = json.load(open(sys.argv[1]))["error"]
assert sys.argv[2] == "422", sys.argv[2]
assert error["code"] == "incomplete_capsule", error
assert error["details"]["missing"][0]["code"] == "entrypoint_missing", error
PY

printf '%s\n' \
  "source_capsule_id=$source_id" \
  "replay_job_id=$job_id" \
  "candidate_capsule_id=$candidate_id" \
  "diagnostic_capsule_id=$diagnostic_id" \
  >"$artifact_root/summary.txt"
echo "typescript-reference-smoke: passed; retained evidence: $artifact_root"
