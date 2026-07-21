#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/lib/worktree.sh"
flight_worktree_init
cd "$FLIGHT_REPO_ROOT"

action="${1:-}"
case "$action" in
  up)
    if "$FLIGHT_REPO_ROOT/scripts/run-with-timeout.py" 240 -- docker compose -f deploy/docker-compose.yml up --detach --build; then
      :
    else
      status=$?
      echo 'dev-stack: image build/start failed or exceeded 240s; check registry access, then inspect scripts/dev-stack.sh logs and rerun make dev-up' >&2
      exit "$status"
    fi
    deadline=$((SECONDS + 180))
    until curl --fail --silent "http://127.0.0.1:${FLIGHT_HTTP_PORT}/healthz" >/dev/null 2>&1; do
      if (( SECONDS >= deadline )); then
        docker compose -f deploy/docker-compose.yml ps >&2
        docker compose -f deploy/docker-compose.yml logs --no-color --tail 200 flight >&2
        echo 'dev-stack: Flight Recorder did not become healthy within 180s; inspect the logs above and run make dev-down' >&2
        exit 1
      fi
      sleep 2
    done
    "$FLIGHT_REPO_ROOT/scripts/investigation-demo.sh" seed
    ;;
  down)
    docker compose -f deploy/docker-compose.yml down --volumes --remove-orphans
    "$FLIGHT_REPO_ROOT/scripts/investigation-demo.sh" cleanup
    ;;
  ps)
    docker compose -f deploy/docker-compose.yml ps
    ;;
  logs)
    docker compose -f deploy/docker-compose.yml logs --no-color --tail "${2:-200}"
    ;;
  --help|-h|help)
    echo 'usage: scripts/dev-stack.sh <up|down|ps|logs [lines]>'
    ;;
  *) echo 'dev-stack: expected up, down, ps, or logs; run scripts/dev-stack.sh --help' >&2; exit 2 ;;
esac
