#!/usr/bin/env bash

set -u -o pipefail

STATE_DIR=${AWSEMPAY_DEV_STATE_DIR:-/tmp/awsempay-dev}
API_COMMAND=${AWSEMPAY_API_COMMAND:-'go run ./cmd/openpay serve'}
DASHBOARD_COMMAND=${AWSEMPAY_DASHBOARD_COMMAND:-'npm --prefix admin run dev'}
API_URL=${AWSEMPAY_API_URL:-http://127.0.0.1:8080/admin/api/console-status}
DASHBOARD_URL=${AWSEMPAY_DASHBOARD_URL:-http://127.0.0.1:3000/login}
API_PORT=${AWSEMPAY_API_PORT:-8080}
DASHBOARD_PORT=${AWSEMPAY_DASHBOARD_PORT:-3000}
TIMEOUT=${AWSEMPAY_DEV_TIMEOUT:-30}
STOP_TIMEOUT=${AWSEMPAY_DEV_STOP_TIMEOUT:-5}

usage() { echo "usage: $0 {start|stop}" >&2; exit 2; }
log_path() { printf '%s/%s.log' "$STATE_DIR" "$1"; }
pid_path() { printf '%s/%s.pid' "$STATE_DIR" "$1"; }
proc_start_time() { awk '{print $22}' "/proc/$1/stat" 2>/dev/null; }
group_is_running() {
  ps -eo pgid=,stat= | awk -v pgid="$1" '$1 == pgid && $2 !~ /^Z/ { found=1 } END { exit !found }'
}

record_is_owned() {
  local service=$1 file pid start marker actual_start cmdline pgid
  file=$(pid_path "$service")
  [ -r "$file" ] || return 1
  read -r pid start marker <"$file" || return 1
  [[ "$pid" =~ ^[0-9]+$ && "$start" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  actual_start=$(proc_start_time "$pid") || return 1
  [ "$actual_start" = "$start" ] || return 1
  cmdline=$(tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null) || return 1
  [[ "$cmdline" == *"$marker"* ]] || return 1
  pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ')
  [ "$pgid" = "$pid" ]
}

port_is_occupied() { ss -ltnH "sport = :$1" 2>/dev/null | grep -q .; }

diagnose_port() {
  local port=$1
  echo "  port :$port is occupied but its service is not ready; it was not stopped" >&2
  ss -ltnp "sport = :$port" 2>/dev/null >&2 || true
  fuser -n tcp "$port" 2>/dev/null >&2 || true
}

wait_until_ready() {
  local service=$1 url=$2 elapsed=0
  while (( elapsed < TIMEOUT * 10 )); do
    if curl --fail --silent --show-error --max-time 1 -o /dev/null "$url" 2>/dev/null; then return 0; fi
    if ! record_is_owned "$service"; then
      echo "  $service exited before it became ready; see $(log_path "$service")" >&2
      return 1
    fi
    sleep 0.1
    ((elapsed += 1))
  done
  echo "  $service did not become ready within ${TIMEOUT}s; see $(log_path "$service")" >&2
  return 1
}

start_service() {
  local service=$1 command=$2 url=$3 port=$4 file marker pid start
  file=$(pid_path "$service")
  if record_is_owned "$service"; then
    if curl --fail --silent --show-error --max-time 1 -o /dev/null "$url" 2>/dev/null; then
      echo "  $service already running (log: $(log_path "$service"))"
      return 0
    fi
    echo "  $service is an owned process but is not ready; see $(log_path "$service")" >&2
    return 1
  fi
  [ ! -e "$file" ] || rm -f "$file"
  if curl --fail --silent --show-error --max-time 1 -o /dev/null "$url" 2>/dev/null; then
    echo "  $service is already healthy but is not managed by this state directory"
    return 0
  fi
  if port_is_occupied "$port"; then diagnose_port "$port"; return 1; fi
  marker="awsempay-dev-${service}-$(date +%s%N)-$$"
  echo "  $service starting (log: $(log_path "$service"))"
  setsid bash -c '
    marker=$1
    shift
    trap : TERM INT
    "$@" &
    child=$!
    while kill -0 "$child" 2>/dev/null; do
      wait "$child"
      result=$?
    done
    while setsid bash -c '\''ps -eo pid=,pgid=,stat= | awk -v leader="$1" '\'\''$2 == leader && $1 != leader && $3 !~ /^Z/ { found=1 } END { exit !found }'\'\'''\'' _ "$$"; do
      sleep 0.1
    done
    exit "${result:-0}"
  ' _ "$marker" bash -c "$command" >"$(log_path "$service")" 2>&1 &
  pid=$!
  start=$(proc_start_time "$pid")
  if [ -z "$start" ]; then echo "  could not record $service process; see $(log_path "$service")" >&2; return 1; fi
  printf '%s %s %s\n' "$pid" "$start" "$marker" >"$file"
  wait_until_ready "$service" "$url"
}

stop_service() {
  local service=$1 file pid elapsed=0
  file=$(pid_path "$service")
  [ -e "$file" ] || return 0
  if ! record_is_owned "$service"; then
    echo "  $service PID record is stale or no longer owned; leaving processes untouched"
    rm -f "$file"
    return 0
  fi
  read -r pid _ <"$file"
  echo "  $service stopping"
  kill -TERM -- "-$pid" 2>/dev/null || true
  while group_is_running "$pid"; do
    if (( elapsed >= STOP_TIMEOUT * 10 )); then
      echo "  $service did not stop within ${STOP_TIMEOUT}s; ownership retained at $file; see $(log_path "$service")" >&2
      return 1
    fi
    sleep 0.1
    ((elapsed += 1))
  done
  rm -f "$file"
}

case ${1:-} in
  start)
    [[ "$TIMEOUT" =~ ^[1-9][0-9]*$ ]] || { echo "AWSEMPAY_DEV_TIMEOUT must be a positive integer" >&2; exit 2; }
    mkdir -p "$STATE_DIR"
    command -v setsid >/dev/null || { echo "setsid is required" >&2; exit 1; }
    start_service api "$API_COMMAND" "$API_URL" "$API_PORT" || exit 1
    start_service dashboard "$DASHBOARD_COMMAND" "$DASHBOARD_URL" "$DASHBOARD_PORT" || exit 1
    ;;
  stop)
    [[ "$STOP_TIMEOUT" =~ ^[1-9][0-9]*$ ]] || { echo "AWSEMPAY_DEV_STOP_TIMEOUT must be a positive integer" >&2; exit 2; }
    result=0
    stop_service api || result=1
    stop_service dashboard || result=1
    exit "$result"
    ;;
  *) usage ;;
esac
