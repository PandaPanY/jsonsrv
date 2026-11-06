#!/usr/bin/env bash
# 并发检查：真实二进制 200 并发 POST 与同一显式 id 40 并发，默认和 --legacy 各一遍。
# 需要 Go、Python 3、curl。只用 127.0.0.1:3031，按 PID 停进程。
set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="${WORK_DIR:-$ROOT/.work}/concurrency"
BIN="$WORK/jsonsrv"
PORT=3031
PROBE="$ROOT/scripts/concurrency-probe.py"
mkdir -p "$WORK"
cd "$ROOT" || exit 2
go build -trimpath -o "$BIN" ./cmd/jsonsrv || exit 1

run_case() {
  local flags="$1" label="$2" body="$3" n="$4"
  local d="$WORK/run"
  rm -rf "$d"; mkdir -p "$d"
  printf '{"posts":[]}\n' > "$d/db.json"
  cd "$d" || return
  # shellcheck disable=SC2086
  "$BIN" db.json --port "$PORT" --quiet $flags >"$WORK/server.log" 2>&1 &
  local pid=$!
  for _ in $(seq 1 40); do curl -s -o /dev/null "http://127.0.0.1:$PORT/posts" && break; sleep 0.2; done
  echo "== $label（$n 并发，模式: ${flags:-默认}）=="
  python3 "$PROBE" "http://127.0.0.1:$PORT" "$n" "$body"
  python3 - "$d/db.json" <<'PY'
import json
import sys
d = json.load(open(sys.argv[1]))
ids = [str(r.get("id")) for r in d["posts"]]
print("  数据文件：记录数=%d 唯一 id 数=%d" % (len(ids), len(set(ids))))
PY
  kill "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null || true
  cd "$ROOT" || return
  echo
}

run_case "" "200 并发 POST" '{"title":"x"}' 200
run_case "--legacy" "200 并发 POST" '{"title":"x"}' 200
run_case "" "同一显式 id 并发" '{"id":7,"title":"dup"}' 40
run_case "--legacy" "同一显式 id 并发" '{"id":7,"title":"dup"}' 40
echo "CONCURRENCY-DONE"
