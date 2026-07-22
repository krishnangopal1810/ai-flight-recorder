#!/usr/bin/env bash
set -euo pipefail

launcher=deploy/demo/flight-demo

"$launcher" --help | grep -q 'flight-demo start'

if "$launcher" start --name 'NOT SAFE' >/dev/null 2>&1; then
  echo 'demo-launcher-test: unsafe names must fail before Docker is called' >&2
  exit 1
fi

if "$launcher" start --http-port 80 >/dev/null 2>&1; then
  echo 'demo-launcher-test: privileged ports must fail before Docker is called' >&2
  exit 1
fi

stub_root=$(mktemp -d -t flight-demo-test.XXXXXX)
trap 'rm -rf "$stub_root"' EXIT
stub="$stub_root/docker"
cat >"$stub" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
  version) exit 0 ;;
  inspect) exit 1 ;;
  ps) exit 0 ;;
  network)
    [[ "${2:-}" == inspect ]] && exit 1
    exit 0
    ;;
  volume)
    [[ "${2:-}" == inspect ]] && exit 1
    [[ "${2:-}" == ls ]] && exit 0
    exit 0
    ;;
esac
exit 0
STUB
chmod +x "$stub"

status=$(AFR_DEMO_DOCKER_BIN="$stub" "$launcher" status --name test --json)
[[ "$(jq -r '.status' <<<"$status")" == absent ]]
[[ "$(jq -r '.running' <<<"$status")" == false ]]

stopped=$(AFR_DEMO_DOCKER_BIN="$stub" "$launcher" stop --name test --json)
[[ "$(jq -r '.status' <<<"$stopped")" == stopped ]]
[[ "$(jq -r '.volumes_retained' <<<"$stopped")" == true ]]

purged=$(AFR_DEMO_DOCKER_BIN="$stub" "$launcher" stop --name test --purge --json)
[[ "$(jq -r '.status' <<<"$purged")" == purged ]]

if AFR_DEMO_DOCKER_BIN="$stub" AFR_DEMO_USE_LOCAL_IMAGES=maybe "$launcher" status --name test >/dev/null 2>&1; then
  echo 'demo-launcher-test: invalid development image mode must fail closed' >&2
  exit 1
fi

echo 'demo-launcher-test: public command and labelled cleanup contracts passed'
