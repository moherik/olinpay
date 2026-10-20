#!/usr/bin/env bash

set -u -o pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DEV_SCRIPT="$ROOT/scripts/dev.sh"
TMPDIR=$(mktemp -d)
PIDS=()

cleanup() {
  for pid in "${PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_eq() {
  [ "$1" = "$2" ] || fail "expected $2, got $1"
}

assert_alive() {
  kill -0 "$1" 2>/dev/null || fail "process $1 is not alive"
}

wait_for_exit() {
  local pid=$1
  local i
  for i in $(seq 1 50); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.05
  done
  fail "process $pid did not exit"
}

port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

cat >"$TMPDIR/fake-service.py" <<'PY'
#!/usr/bin/env python3
import http.server
import sys
import time

mode, port = sys.argv[1], int(sys.argv[2])
if mode == "exit":
    sys.exit(7)
if mode == "never":
    time.sleep(30)
    sys.exit(0)
if mode == "delayed":
    time.sleep(float(sys.argv[3]))

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(503 if mode == "unhealthy" else 200)
        self.end_headers()
    def log_message(self, format, *args):
        pass

http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
chmod +x "$TMPDIR/fake-service.py"

run_dev() {
  local state=$1 api_port=$2 dashboard_port=$3
  shift 3
  AWSEMPAY_DEV_STATE_DIR="$state" \
  AWSEMPAY_API_PORT="$api_port" \
  AWSEMPAY_DASHBOARD_PORT="$dashboard_port" \
  AWSEMPAY_API_URL="http://127.0.0.1:$api_port/ready" \
  AWSEMPAY_DASHBOARD_URL="http://127.0.0.1:$dashboard_port/ready" \
  AWSEMPAY_DEV_TIMEOUT=3 \
  "$@"
}

service_command() {
  local mode=$1 service_port=$2 counter=$3 extra=${4:-}
  printf 'printf x >> %q; exec %q %q %q %s %s' "$counter" "$TMPDIR/fake-service.py" "$mode" "$service_port" "$extra" ""
}

scenario_healthy_reuse() {
  local state="$TMPDIR/reuse" api_port dashboard_port api_cmd dashboard_cmd
  state="$TMPDIR/reuse"; api_port=$(port); dashboard_port=$(port)
  api_cmd=$(service_command healthy "$api_port" "$state/api-launches")
  dashboard_cmd=$(service_command healthy "$dashboard_port" "$state/dashboard-launches")
  mkdir -p "$state"
  AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start || fail "healthy start failed"
  AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start || fail "healthy reuse failed"
  assert_eq "$(wc -c <"$state/api-launches")" 1
  assert_eq "$(wc -c <"$state/dashboard-launches")" 1
  run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" stop || fail "stop failed"
}

scenario_occupied_unhealthy_port() {
  local state="$TMPDIR/occupied" api_port dashboard_port api_cmd dashboard_cmd external
  state="$TMPDIR/occupied"; api_port=$(port); dashboard_port=$(port)
  "$TMPDIR/fake-service.py" unhealthy "$api_port" & external=$!; PIDS+=("$external")
  sleep 0.1
  mkdir -p "$state"
  api_cmd=$(service_command healthy "$api_port" "$state/api-launches")
  dashboard_cmd=$(service_command healthy "$dashboard_port" "$state/dashboard-launches")
  if AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start; then
    fail "occupied unhealthy port unexpectedly started"
  fi
  [ ! -e "$state/api-launches" ] || fail "launched over an unowned port"
  assert_alive "$external"
}

scenario_delayed_readiness() {
  local state="$TMPDIR/delayed" api_port dashboard_port api_cmd dashboard_cmd
  state="$TMPDIR/delayed"; api_port=$(port); dashboard_port=$(port); mkdir -p "$state"
  api_cmd=$(service_command delayed "$api_port" "$state/api-launches" 0.3)
  dashboard_cmd=$(service_command delayed "$dashboard_port" "$state/dashboard-launches" 0.3)
  AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start || fail "delayed readiness failed"
  run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" stop || fail "delayed stop failed"
}

scenario_early_exit_reports_log() {
  local state="$TMPDIR/exit" api_port dashboard_port api_cmd dashboard_cmd output
  state="$TMPDIR/exit"; api_port=$(port); dashboard_port=$(port); mkdir -p "$state"
  api_cmd=$(service_command exit "$api_port" "$state/api-launches")
  dashboard_cmd=$(service_command healthy "$dashboard_port" "$state/dashboard-launches")
  if output=$(AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start 2>&1); then
    fail "early exit unexpectedly started"
  fi
  [[ "$output" == *"$state/api.log"* ]] || fail "early exit did not report API log path"
}

scenario_timeout() {
  local state="$TMPDIR/timeout" api_port dashboard_port api_cmd dashboard_cmd
  state="$TMPDIR/timeout"; api_port=$(port); dashboard_port=$(port); mkdir -p "$state"
  api_cmd=$(service_command never "$api_port" "$state/api-launches")
  dashboard_cmd=$(service_command healthy "$dashboard_port" "$state/dashboard-launches")
  if AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start; then
    fail "timeout unexpectedly started"
  fi
  run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" stop || fail "timeout stop failed"
}

scenario_stale_pid_replaced() {
  local state="$TMPDIR/stale" api_port dashboard_port api_cmd dashboard_cmd pid
  state="$TMPDIR/stale"; api_port=$(port); dashboard_port=$(port); mkdir -p "$state"
  printf '999999 0 awsempay-dev-api-stale\n' >"$state/api.pid"
  api_cmd=$(service_command healthy "$api_port" "$state/api-launches")
  dashboard_cmd=$(service_command healthy "$dashboard_port" "$state/dashboard-launches")
  AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start || fail "stale pid replacement failed"
  read -r pid _ <"$state/api.pid"
  [ "$pid" != 999999 ] || fail "stale PID was reused"
  run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" stop || fail "stale stop failed"
}

scenario_stop_only_owned_group() {
  local state="$TMPDIR/stop" api_port dashboard_port api_cmd dashboard_cmd api_pid unrelated
  state="$TMPDIR/stop"; api_port=$(port); dashboard_port=$(port); mkdir -p "$state"
  api_cmd=$(service_command healthy "$api_port" "$state/api-launches")
  dashboard_cmd=$(service_command healthy "$dashboard_port" "$state/dashboard-launches")
  AWSEMPAY_API_COMMAND="$api_cmd" AWSEMPAY_DASHBOARD_COMMAND="$dashboard_cmd" \
    run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" start || fail "owned start failed"
  read -r api_pid _ <"$state/api.pid"
  sleep 30 & unrelated=$!; PIDS+=("$unrelated")
  run_dev "$state" "$api_port" "$dashboard_port" bash "$DEV_SCRIPT" stop || fail "owned stop failed"
  wait_for_exit "$api_pid"
  assert_alive "$unrelated"
}

scenario_healthy_reuse
scenario_occupied_unhealthy_port
scenario_delayed_readiness
scenario_early_exit_reports_log
scenario_timeout
scenario_stale_pid_replaced
scenario_stop_only_owned_group

echo "PASS: development process supervision scenarios"
