#!/bin/sh
# Isolated integration test. Never reloads or edits the live OpenClash configuration.
set -eu

guard_bin="${1:-/usr/bin/node-guard}"
test_dir="$(mktemp -d /tmp/node-guard-smoke.XXXXXX)"
core_pid=""
cleanup() {
    if [ -n "$core_pid" ]; then
        kill "$core_pid" 2>/dev/null || true
        wait "$core_pid" 2>/dev/null || true
    fi
    rm -rf "$test_dir"
}
trap cleanup EXIT INT TERM

if curl -s --connect-timeout 1 --max-time 1 http://127.0.0.1:19091/version >/dev/null; then
    echo 'Test port 19091 already in use' >&2
    exit 1
fi

cat > "$test_dir/core.yaml" <<'YAML'
mixed-port: 0
external-controller: 127.0.0.1:19091
allow-lan: false
mode: rule
log-level: warning
profile:
  store-selected: false
dns:
  enable: false
proxies:
  - name: BAD
    type: http
    server: 127.0.0.1
    port: 1
  - name: GOOD
    type: http
    server: 127.0.0.1
    port: 7893
proxy-groups:
  - name: SMOKE
    type: select
    proxies: [BAD, GOOD]
rules:
  - MATCH,SMOKE
YAML

# Reuse the configured loopback HTTP proxy, including its optional authentication.
# Credentials remain only in the router's private temporary directory.
ruby -ryaml -e '
  live=YAML.load_file("/etc/openclash/config.yaml")
  test=YAML.load_file(ARGV[0])
  good=test["proxies"].find { |p| p["name"]=="GOOD" }
  good["port"]=live.fetch("mixed-port")
  auth=live.fetch("authentication",[]).first
  if auth
    user,password=auth.split(":",2)
    good["username"]=user
    good["password"]=password.to_s
  end
  File.write(ARGV[0],YAML.dump(test))
' "$test_dir/core.yaml"

/usr/bin/mihomo -d "$test_dir" -f "$test_dir/core.yaml" > "$test_dir/core.log" 2>&1 &
core_pid=$!
ready=0
for attempt in 1 2 3 4 5; do
    if ! kill -0 "$core_pid" 2>/dev/null; then cat "$test_dir/core.log" >&2; exit 1; fi
    if curl -fsS --max-time 1 http://127.0.0.1:19091/version >/dev/null 2>&1; then ready=1; break; fi
    sleep 1
done
[ "$ready" = 1 ] || { echo 'Isolated core did not start' >&2; exit 1; }

cat > "$test_dir/guard.json" <<JSON
{
  "controller": "http://127.0.0.1:19091",
  "groups": ["SMOKE"],
  "timeout_ms": 2000,
  "status_file": "$test_dir/status.json"
}
JSON

# One live loop: the first failed probe schedules GOOD for the next second.
timeout -s TERM 4 "$guard_bin" -config "$test_dir/guard.json" > "$test_dir/guard.log" 2>&1 || true
cp "$test_dir/status.json" "$test_dir/result.json"
count="$(jsonfilter -i "$test_dir/result.json" -e '@.switches')"
selected="$(jsonfilter -i "$test_dir/result.json" -e '@.groups[0].selected')"
if [ "$count" != 1 ] || [ "$selected" != GOOD ]; then
    cat "$test_dir/result.json" >&2
    exit 1
fi
echo 'PASS: isolated Mihomo switched BAD -> GOOD; live OpenClash configuration untouched'
