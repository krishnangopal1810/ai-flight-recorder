#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/lib/worktree.sh"
flight_worktree_init

case "${1:---json}" in
  --json)
    printf '{"worktree_id":"%s","compose_project":"%s","http_port":%d,"otlp_grpc_port":%d,"otlp_http_port":%d,"artifact_root":"%s"}\n' \
      "$FLIGHT_WORKTREE_ID" "$COMPOSE_PROJECT_NAME" "$FLIGHT_HTTP_PORT" "$FLIGHT_OTLP_GRPC_PORT" "$FLIGHT_OTLP_HTTP_PORT" "$FLIGHT_ARTIFACT_ROOT"
    ;;
  --shell)
    printf 'export FLIGHT_WORKTREE_ID=%q\n' "$FLIGHT_WORKTREE_ID"
    printf 'export COMPOSE_PROJECT_NAME=%q\n' "$COMPOSE_PROJECT_NAME"
    printf 'export FLIGHT_HTTP_PORT=%q\n' "$FLIGHT_HTTP_PORT"
    printf 'export FLIGHT_OTLP_GRPC_PORT=%q\n' "$FLIGHT_OTLP_GRPC_PORT"
    printf 'export FLIGHT_OTLP_HTTP_PORT=%q\n' "$FLIGHT_OTLP_HTTP_PORT"
    printf 'export FLIGHT_ARTIFACT_ROOT=%q\n' "$FLIGHT_ARTIFACT_ROOT"
    ;;
  --help|-h)
    echo 'usage: scripts/worktree-env.sh [--json|--shell]'
    ;;
  *)
    echo 'worktree-env: expected --json or --shell; run scripts/worktree-env.sh --help' >&2
    exit 2
    ;;
esac
