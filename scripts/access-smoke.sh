#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  echo 'usage: scripts/access-smoke.sh <artifact-directory>'
  echo 'Proves live unauthenticated rejection plus native and trusted-proxy TLS access contracts.'
  exit 0
fi
if [[ $# -ne 1 ]]; then
  echo 'access-smoke: expected one artifact directory; run scripts/access-smoke.sh --help' >&2
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

unauthenticated_status=$(curl --silent --show-error --output "$artifact_root/api-unauthenticated.json" --write-out '%{http_code}' \
  "$base_url/api/v1/capsules?limit=1" 2>"$artifact_root/api-unauthenticated-stderr.log")
metrics_status=$(curl --silent --show-error --output "$artifact_root/metrics-unauthenticated.json" --write-out '%{http_code}' \
  "$base_url/metrics" 2>"$artifact_root/metrics-unauthenticated-stderr.log")
otlp_status=$(curl --silent --show-error --output "$artifact_root/otlp-unauthenticated.json" --write-out '%{http_code}' \
  -H 'Content-Type: application/x-protobuf' --data-binary '' "$base_url/v1/traces" 2>"$artifact_root/otlp-unauthenticated-stderr.log")
if [[ "$unauthenticated_status" != 401 || "$metrics_status" != 401 || "$otlp_status" != 401 ]]; then
  echo "access-smoke: unauthenticated statuses api=$unauthenticated_status metrics=$metrics_status otlp=$otlp_status, want 401" >&2
  exit 1
fi

curl --fail --silent --show-error -H "Authorization: Bearer ${token}" \
  "$base_url/api/v1/capsules?limit=1" >"$artifact_root/api-authorized.json" 2>"$artifact_root/api-authorized-stderr.log"
curl --fail --silent --show-error -H "Authorization: Bearer ${token}" \
  "$base_url/metrics" >"$artifact_root/metrics-authorized.txt" 2>"$artifact_root/metrics-authorized-stderr.log"

go test -count=1 -tags=security -run 'TestAFRSEC003(NativeTLSAndScopedAuthentication|TLSProxyRejectsUnauthenticatedAndDirectRequests)$' \
  -v ./tests/security >"$artifact_root/tls-contract.log" 2>"$artifact_root/tls-contract-stderr.log"

python3 - "$artifact_root" <<'PY'
import json, pathlib, sys

root = pathlib.Path(sys.argv[1])
for name in ("api-unauthenticated.json", "metrics-unauthenticated.json", "otlp-unauthenticated.json"):
    envelope = json.loads((root / name).read_text())
    assert envelope["error"]["code"] == "authentication", (name, envelope)
contract = (root / "tls-contract.log").read_text()
assert '"mode":"native_tls"' in contract, contract
assert '"mode":"trusted_tls_proxy"' in contract, contract
assert '--- PASS: TestAFRSEC003NativeTLSAndScopedAuthentication' in contract, contract
assert '--- PASS: TestAFRSEC003TLSProxyRejectsUnauthenticatedAndDirectRequests' in contract, contract
PY

printf '{"status":"passed","live_api":401,"live_metrics":401,"live_otlp_http":401,"native_tls":"passed","proxy_tls":"passed","reproduce":"scripts/access-smoke.sh <artifact-directory>"}\n' >"$artifact_root/status.json"
echo "access-smoke: passed; retained evidence: $artifact_root"
