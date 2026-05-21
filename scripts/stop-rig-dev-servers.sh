#!/usr/bin/env bash
#
# stop-rig-dev-servers.sh — free localhost ports before go run / QA smoke
#
# Stops listeners on given TCP ports and stray `go run … cmd/server` processes.
# Does not touch protected town ports (Dolt 3307, NATS 4222, LLM 11434).
#
# gt-agent runs equivalent cleanup automatically for polecat/QA; use this when
# the LLM needs an explicit CMD after "address already in use".
#
# Usage:
#   ./scripts/stop-rig-dev-servers.sh           # default port 8080
#   ./scripts/stop-rig-dev-servers.sh 8080 9090
#
set -euo pipefail

PROTECTED=(3307 4222 11434)

is_protected() {
  local p="$1"
  for x in "${PROTECTED[@]}"; do
    [[ "$p" == "$x" ]] && return 0
  done
  return 1
}

kill_port() {
  local port="$1"
  if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
    echo "invalid port: $port" >&2
    return 1
  fi
  if is_protected "$port"; then
    echo "refusing protected port: $port" >&2
    return 1
  fi
  if command -v fuser >/dev/null 2>&1; then
    fuser -k "${port}/tcp" 2>/dev/null || true
    return 0
  fi
  if command -v lsof >/dev/null 2>&1; then
    local pids
    pids="$(lsof -ti ":${port}" 2>/dev/null || true)"
    if [[ -n "${pids:-}" ]]; then
      # shellcheck disable=SC2086
      kill -TERM $pids 2>/dev/null || true
    fi
    return 0
  fi
  echo "need fuser or lsof to free port $port" >&2
  return 1
}

kill_go_run_servers() {
  if ! command -v pkill >/dev/null 2>&1; then
    return 0
  fi
  pkill -f 'go run.*cmd/server' 2>/dev/null || true
  pkill -f 'go run.*/server/main' 2>/dev/null || true
}

PORTS=("$@")
if [[ ${#PORTS[@]} -eq 0 ]]; then
  PORTS=(8080)
fi

for port in "${PORTS[@]}"; do
  kill_port "$port"
done
kill_go_run_servers
