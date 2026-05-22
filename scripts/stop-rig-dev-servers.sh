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

kill_port_lsof() {
  local port="$1"
  local pids sig
  for sig in TERM KILL; do
    pids="$(lsof -ti ":${port}" 2>/dev/null || true)"
    if [[ -z "${pids:-}" ]]; then
      return 0
    fi
    # shellcheck disable=SC2086
    kill -"${sig}" $pids 2>/dev/null || true
    sleep 0.1
  done
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
  # lsof+kill works on macOS and Linux (busybox fuser often lacks -k).
  if command -v lsof >/dev/null 2>&1; then
    kill_port_lsof "$port"
    return 0
  fi
  echo "need lsof to free port $port" >&2
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
