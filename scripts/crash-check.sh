#!/usr/bin/env bash
# 崩溃检查：大库写入过程中反复 kill -9 共 6 轮，断言数据文件完好、每轮确实留下本数据文件的
# 临时残留；再正常启动一次，断言残留清零、其他数据文件的临时文件未被删除、启动日志打印清理提示。
# 需要 Go、Python 3、curl。只用 127.0.0.1:3045，按 PID 停进程（kill -9 是场景本身）。
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="${WORK_DIR:-$ROOT/.work}/crash"
BIN="$WORK/jsonsrv"
PORT=${PORT:-3045}
cd "$ROOT" || exit 2
rm -rf "$WORK"; mkdir -p "$WORK/data"
go build -o "$BIN" ./cmd/jsonsrv && echo BUILD-OK

DATA="$WORK/data"
python3 "$ROOT/scripts/gen-perf-db.py" "$DATA/db.json"
# 另一数据文件的临时残留（300KB 即可），启动清理后必须原样存活：
head -c 300000 /dev/zero > "$DATA/.jsonsrv-other.json-000001.tmp"

FAILED=0
countmine() { ls -1A "$1" | grep -c '^\.jsonsrv-db\.json-.*\.tmp$' || true; }
countother() { ls -1A "$1" | grep -c '^\.jsonsrv-other\.json-.*\.tmp$' || true; }

for i in 1 2 3 4 5 6; do
  "$BIN" "$DATA/db.json" --port "$PORT" >"$WORK/run-$i.log" 2>&1 &
  P=$!
  for _ in $(seq 1 80); do curl -s -o /dev/null "http://127.0.0.1:$PORT/posts/1" && break; sleep 0.25; done
  # 后台连续 POST（POST 同步落盘约 250ms 一次，14MB 序列化+改名）；1 秒后 kill -9
  # 必落在某次 POST 的落盘窗口内（临时文件已创建、改名前）。
  ( for k in $(seq 1 8); do
      curl -s -o /dev/null -X POST -H 'Content-Type: application/json' \
        -d "{\"title\":\"crash-$i-$k\"}" "http://127.0.0.1:$PORT/posts"
    done ) >/dev/null 2>&1 &
  CP=$!
  sleep 1
  kill -9 "$P" 2>/dev/null || true
  kill "$CP" 2>/dev/null || true
  wait 2>/dev/null || true
  OK=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(len(d["posts"]))' "$DATA/db.json" 2>/dev/null) || OK=CORRUPT
  echo "轮 $i: kill -9 后 posts=$( [ "$OK" = CORRUPT ] && echo 损坏 || echo "完好($OK,起点100000+递增)" ) 本数据文件残留=$(countmine "$DATA") 其他残留=$(countother "$DATA")"
  if [ "$OK" = CORRUPT ]; then echo "FAIL 轮 $i 数据文件被 kill -9 损坏"; FAILED=1; fi
done

LEFT=$(countmine "$DATA")
echo "6 轮 kill -9 后：本数据文件残留=$LEFT（应 >0，证明崩溃确实留下临时文件）"
if [ "$LEFT" -gt 0 ]; then :; else echo "FAIL kill -9 未产生残留，检查前提不成立"; FAILED=1; fi

echo "== 正常启动一次（应打印清理提示并把残留清零） =="
"$BIN" "$DATA/db.json" --port "$PORT" >"$WORK/final.log" 2>&1 &
P=$!
for _ in $(seq 1 80); do curl -s -o /dev/null "http://127.0.0.1:$PORT/posts/1" && break; sleep 0.25; done
kill "$P" 2>/dev/null || true
wait 2>/dev/null || true
echo "--- 启动日志: ---"
cat "$WORK/final.log"
AFTER=$(countmine "$DATA")
echo "正常启动后：本数据文件残留=$AFTER（必须为 0）"
if [ "$AFTER" = 0 ]; then :; else echo "FAIL 残留未清零"; FAILED=1; fi
grep -q 'removed .* temporary file' "$WORK/final.log" || { echo "FAIL 启动日志缺清理提示"; FAILED=1; }
OTHER=$(countother "$DATA")
echo "其他数据文件临时文件仍在=$OTHER（必须为 1）"
if [ "$OTHER" = 1 ]; then :; else echo "FAIL 其他数据文件的临时文件被误删"; FAILED=1; fi

echo "== 崩溃检查结束，用时 ${SECONDS}s，FAILED=$FAILED =="
[ "$FAILED" = 0 ] && echo "CRASH-OK"
