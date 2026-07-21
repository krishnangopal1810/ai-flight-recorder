#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/playback-offline-smoke.sh [artifact-directory]'
  echo 'Cross-compiles the playback acceptance test and runs it in a read-only OCI container with networking disabled.'
  exit 0
fi
if [[ $# -gt 1 ]]; then
  echo 'playback-offline-smoke: expected at most one artifact directory' >&2
  exit 2
fi

root=$(git rev-parse --show-toplevel)
cd "$root"
artifact_root="${1:-${FLIGHT_PLAYBACK_ARTIFACT_ROOT:-${root}/.artifacts/playback-offline}}"
temporary_root=$(mktemp -d -t flight-playback-offline.XXXXXX)
container_id=""
staging_id=""
test_volume=""
cleanup() {
  local exit_code=$?
  set +e
  if [[ -n "$container_id" ]]; then
    docker rm -f "$container_id" >/dev/null 2>&1
  fi
  if [[ -n "$staging_id" ]]; then
    docker rm -f "$staging_id" >/dev/null 2>&1
  fi
  if [[ -n "$test_volume" ]]; then
    docker volume rm -f "$test_volume" >/dev/null 2>&1
  fi
  rm -rf "$temporary_root"
  exit "$exit_code"
}
trap cleanup EXIT

mkdir -p "$artifact_root"
engine_arch=$(docker info --format '{{.Architecture}}')
case "$engine_arch" in
  amd64|x86_64) go_arch=amd64 ;;
  arm64|aarch64) go_arch=arm64 ;;
  *) echo "playback-offline-smoke: unsupported OCI engine architecture ${engine_arch}" >&2; exit 1 ;;
esac

GOOS=linux GOARCH="$go_arch" CGO_ENABLED=0 go test -c -o "$temporary_root/playback.test" ./internal/service
chmod 755 "$temporary_root/playback.test"
test_volume=$(docker volume create)
staging_id=$(docker create --mount "type=volume,src=${test_volume},dst=/stage" --entrypoint /flight ai-flight-recorder:local --help)
docker cp "$temporary_root/playback.test" "$staging_id:/stage/playback.test"
docker rm "$staging_id" >/dev/null
staging_id=""
container_id=$(docker create \
  --network none \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --user 65532:65532 \
  --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  --mount "type=volume,src=${test_volume},dst=/playback,readonly" \
  --entrypoint /playback/playback.test \
  ai-flight-recorder:local \
  -test.v -test.run '^TestAFRPLY001Playback(KeepsVerifiedEvidenceInspectableWithDamagedPayloads|NeverExecutesEntrypointOrUsesOutboundTransport)$')
docker inspect --format '{"network_mode":"{{.HostConfig.NetworkMode}}","read_only_rootfs":{{.HostConfig.ReadonlyRootfs}},"user":"{{.Config.User}}","cap_drop":{{json .HostConfig.CapDrop}},"security_opt":{{json .HostConfig.SecurityOpt}}}' "$container_id" >"$artifact_root/isolation.json"
python3 - "$artifact_root/isolation.json" <<'PY'
import json, sys
evidence = json.load(open(sys.argv[1]))
assert evidence["network_mode"] == "none", evidence
assert evidence["read_only_rootfs"] is True, evidence
assert evidence["user"] == "65532:65532", evidence
assert "ALL" in (evidence["cap_drop"] or []), evidence
assert "no-new-privileges" in (evidence["security_opt"] or []), evidence
PY
if ! docker start -a "$container_id" >"$artifact_root/test.log" 2>&1; then
  cat "$artifact_root/test.log" >&2
  exit 1
fi
cat "$artifact_root/test.log"
printf '{"status":"passed","network_mode":"none","application_processes_started":0,"outbound_requests":0}\n' >"$artifact_root/status.json"
echo "playback-offline-smoke: passed; retained evidence: $artifact_root"
