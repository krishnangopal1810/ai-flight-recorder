#!/usr/bin/env bash
set -euo pipefail

fail=0
check_forbidden() {
  local path="$1" pattern="$2" remediation="$3"
  if rg -n "$pattern" "$path" --glob '*.go' >/tmp/flight-architecture-check 2>/dev/null; then
    cat /tmp/flight-architecture-check
    echo "architecture-check: $remediation" >&2
    fail=1
  fi
}

check_forbidden internal/types 'internal/(config|repository|service|runtime)' 'types may not depend on later layers; move the behavior to the owning layer.'
check_forbidden internal/config 'internal/(repository|service|runtime)' 'config may only depend on types and providers.'
check_forbidden internal/repository 'internal/(service|runtime)' 'repositories may not depend on services or runtimes.'
check_forbidden internal/service 'internal/runtime' 'services must not depend on HTTP, CLI, OTLP, or worker runtimes.'
check_forbidden internal/service/playback.go '"(net|net/http|os/exec|github.com/krishnangopal1810/ai-flight-recorder/internal/providers)' 'playback is passive evidence projection; use only the repository and typed values, never network, process, or runtime providers.'
check_forbidden internal/runtime 'database/sql|modernc.org/sqlite|pgx|minio-go' 'runtime code must call a service instead of accessing storage directly.'

if rg -n 'log\.(Print|Printf|Println|Fatal|Fatalf|Panic|Panicf)' --glob '*.go' --glob '!scripts/**' . >/tmp/flight-log-check 2>/dev/null; then
  cat /tmp/flight-log-check
  echo 'architecture-check: use structured slog with safe typed attributes.' >&2
  fail=1
fi

if rg -n 'os\.(Getenv|LookupEnv)' internal cmd --glob '*.go' --glob '!internal/config/**' >/tmp/flight-env-check 2>/dev/null; then
  cat /tmp/flight-env-check
  echo 'architecture-check: read environment variables through the typed config provider.' >&2
  fail=1
fi

while read -r lines file; do
  if [[ "$lines" -gt 650 ]]; then
    echo "$file:$lines"
    echo 'architecture-check: split oversized source files at a domain or boundary seam (maximum 650 lines).' >&2
    fail=1
  fi
done < <(find internal cmd -name '*.go' -type f -exec wc -l {} + | awk '$2 != "total" {print $1, $2}')

if rg -n 'logger\.(Info|Warn|Error|Debug)\([^\n]*(prompt|payload|input|output|document|repository_url)' internal cmd --glob '*.go' >/tmp/flight-telemetry-check 2>/dev/null; then
  cat /tmp/flight-telemetry-check
  echo 'architecture-check: product telemetry may contain counts/durations/codes, never capsule payload fields.' >&2
  fail=1
fi

if rg -n '(^|[^A-Za-z])fetch\(' web/src --glob '*.ts' --glob '*.tsx' --glob '!api.ts' >/tmp/flight-ui-client-check 2>/dev/null; then
  cat /tmp/flight-ui-client-check
  echo 'architecture-check: inspector network access must go through the typed OpenAPI client boundary in api.ts.' >&2
  fail=1
fi

exit "$fail"
