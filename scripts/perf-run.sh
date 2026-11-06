#!/usr/bin/env bash
# 性能验收：真实二进制 + HTTP，10 万 posts + 2 万 comments。需要 Linux（内存读 /proc 的 VmHWM）、Go、Python 3、curl。
# 每条查询断言结果条数（空结果判 FAIL），计时取 3 次的中位数。任一项超阈值退出码非零；
# 两条嵌套字段排序目前卡在阈值上或超标，见 docs/COMPATIBILITY.md「已知限制」。
# 只用 127.0.0.1:3031-3034，按 PID 停进程。
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT"
WORK="${WORK_DIR:-$ROOT/.work}/perf"
BIN="$WORK/jsonsrv"
PORT_DEFAULT=${PORT_DEFAULT:-3031}
PORT_LEGACY=${PORT_LEGACY:-3033}
PORT_SMALL=${PORT_SMALL:-3032}
PORT_RW=${PORT_RW:-3034}
START=$(date +%s)

mkdir -p "$WORK"
cd "$SRC" || exit 2
echo "== 构建真实二进制 =="
go build -trimpath -o "$BIN" ./cmd/jsonsrv && echo BUILD-OK

echo "== 生成数据（10 万 posts + 2 万 comments） =="
python3 "$SRC/scripts/gen-perf-db.py" "$WORK/perf.db.json"
ls -l "$WORK/perf.db.json"

PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT

start_server() { # start_server <数据文件> <端口> [附加参数...]
  local db=$1 port=$2; shift 2
  rm -f "$WORK/perf-$port.log"
  "$BIN" "$db" --port "$port" "$@" >"$WORK/perf-$port.log" 2>&1 &
  echo $!
}

wait_ready() { # wait_ready <端口>
  local port=$1
  for _ in $(seq 1 80); do
    if curl -s -o /dev/null "http://127.0.0.1:$port/posts/1"; then return 0; fi
    sleep 0.25
  done
  echo "服务 $port 未就绪" >&2; return 1
}

hwm_mb() { # hwm_mb <pid>：VmHWM 峰值驻留（kB → MB，整数）。
  awk '/VmHWM/{printf "%d", $2/1024}' "/proc/$1/status"
}

check_ms() { # check_ms <名称> <毫秒> <阈值毫秒>
  if [ "$2" -le "$3" ]; then echo "PASS $1: ${2}ms (阈值 ${3}ms)"; else echo "FAIL $1: ${2}ms (阈值 ${3}ms)"; FAILED=1; fi
}
check_mb() { # check_mb <名称> <MB> <阈值MB>（VmHWM 口径）
  if [ "$2" -le "$3" ]; then echo "PASS $1: ${2}MB (阈值 ${3}MB, VmHWM)"; else echo "FAIL $1: ${2}MB (阈值 ${3}MB, VmHWM)"; FAILED=1; fi
}

# q()：每条查询 3 次计时取中位数 + 响应体条数断言（scripts/perf-assert.py）。
# 断言失败或超阈值任一记 FAIL。结果条数为 0 而期望 >0 自然断言 FAIL（空结果判 FAIL）。
q() { # q <阈值毫秒> <断言模式> <期望值> <URL> <名称>
  local thr=$1 mode=$2 exp=$3 url=$4 name=$5
  local body="$WORK/perf-body.json" tf="$WORK/perf-times.txt"
  : >"$tf"
  for _ in 1 2 3; do curl -s -o "$body" -w '%{time_total}\n' "$url" >>"$tf"; done
  local ms cnt st
  ms=$(sort -n "$tf" | awk 'NR==2{printf "%d", $1*1000}')
  if cnt=$(python3 "$SRC/scripts/perf-assert.py" "$body" "$mode" "$exp")
  then st=PASS; else st=FAIL; fi
  if [ "$ms" -gt "$thr" ]; then st=FAIL; fi
  echo "$st $name: 中位数 ${ms}ms (阈值 ${thr}ms); $cnt"
  [ "$st" = FAIL ] && FAILED=1
  return 0
}

FAILED=0

# ---- 默认（v1）模式 ----
cp "$WORK/perf.db.json" "$WORK/default.db.json"
PID_DEF=$(start_server "$WORK/default.db.json" "$PORT_DEFAULT"); PIDS+=("$PID_DEF")
wait_ready "$PORT_DEFAULT"
D="http://127.0.0.1:$PORT_DEFAULT"

# 预热（编译路径、缓存），再计时
curl -s -o /dev/null "$D/posts/2"
# 空闲内存口径 = 数据加载后、任何查询之前（防止每请求深拷贝）。
# 排序类查询会真实匹配 10 万条并排序，运行期高水位由下方 3 次 POST 峰值 400MB 闸门保护。
echo "== 空闲内存（加载后、查询前，阈值 200MB） =="
check_mb "默认模式空闲 VmHWM" "$(hwm_mb "$PID_DEF")" 200

echo "== 查询（默认模式，3 次中位数 + 结果条数断言） =="
q 100 items 100 "$D/posts?author.name=user500&_sort=-views&_page=2&_per_page=10" "GET author.name=user500&_sort=-views&_page=2&_per_page=10"
q 100 items_ge 99000 "$D/posts?views:gt=100&_sort=-views&_page=2&_per_page=100" "GET views:gt=100&_sort=-views&_page=2&_per_page=100"
q 100 items_ge 49000 "$D/posts?views:gte=50000&_sort=title&_page=3&_per_page=50" "GET views:gte=50000&_sort=title&_page=3&_per_page=50"
q 100 items_gt0 0 "$D/posts?title:contains=9&_sort=-title&_page=1&_per_page=10" "GET title:contains=9&_sort=-title&_page=1&_per_page=10"
q 100 len 10 "$D/posts?_sort=views&_order=desc&_limit=10" "GET _sort=views&_order=desc&_limit=10（0.17 写法）"
q 100 items 100000 "$D/posts?_sort=author.name,-views&_page=2&_per_page=100" "GET 多字段排序 _sort=author.name,-views&_page=2&_per_page=100"
q 100 items 100000 "$D/posts?_sort=author.name&_page=2&_per_page=100" "GET 单字段嵌套排序 _sort=author.name&_page=2&_per_page=100"
q 200 data_len 10 "$D/posts?_embed=comments&_page=1&_per_page=10" "GET _embed=comments&_page=1&_per_page=10"

echo "== 其他查询（同阈值） =="
q 100 len 100 "$D/posts?author.name=user500" "GET /posts?author.name=user500（无 _page，裸数组）"
q 100 data_len 10 "$D/posts?_sort=views&_order=desc&_page=3" "GET /posts?_sort=views&_order=desc&_page=3"
q 200 data_len 20 "$D/posts?_page=3&_per_page=20&_embed=comments" "GET /posts?_page=3&_per_page=20&_embed=comments"

echo "== 单次 POST（阈值 300ms，共 3 次；3 次后峰值 VmHWM 阈值 400MB） =="
PEAK=0
for i in 1 2 3; do
  T=$(curl -s -o /dev/null -w '%{time_total}' -X POST -H 'Content-Type: application/json' \
    -d '{"title":"perf","views":1}' "$D/posts")
  MS=$(awk -v t="$T" 'BEGIN{printf "%d", t*1000}')
  R=$(hwm_mb "$PID_DEF")
  [ "$R" -gt "$PEAK" ] && PEAK=$R
  echo "POST #$i: ${MS}ms, VmHWM=${R}MB"
  check_ms "POST #$i" "$MS" 300
done
check_mb "3 次 POST 后峰值 VmHWM" "$PEAK" 400

# ---- 读写混合（连续 POST 落盘期间 GET 不得被阻塞） ----
# 10 万数据、后台连续 POST（每个 POST 都整库落盘），前台 GET /posts/1 采样持续到 POST 风暴结束。
# 分位数按样本排序后取最近秩（p50 即中位数）。阈值：GET p50<=20ms p90<=50ms 最大<=150ms；
# POST 平均<=300ms。
cp "$WORK/perf.db.json" "$WORK/rw.db.json"
PID_RW=$(start_server "$WORK/rw.db.json" "$PORT_RW"); PIDS+=("$PID_RW")
wait_ready "$PORT_RW"
RW="http://127.0.0.1:$PORT_RW"
curl -s -o /dev/null "$RW/posts/1"
GET_F="$WORK/rw-get.txt"; POST_F="$WORK/rw-post.txt"
rm -f "$GET_F" "$POST_F"
(
  for i in $(seq 1 60); do
    curl -s -o /dev/null -w '%{time_total}\n' -X POST -H 'Content-Type: application/json' \
      -d "{\"title\":\"rw-$i\",\"views\":1}" "$RW/posts" >>"$POST_F"
  done
) &
PID_POST=$!
# GET 采样持续到 POST 风暴结束（每样本一行，50ms 间隔，样本均匀覆盖落盘窗口）。
while kill -0 "$PID_POST" 2>/dev/null; do
  curl -s -o /dev/null -w '%{time_total}\n' "$RW/posts/1" >>"$GET_F"
  sleep 0.05
done
wait "$PID_POST"
echo "== 读写混合：GET /posts/1 p50<=20ms p90<=50ms max<=150ms；POST avg<=300ms =="
python3 - "$GET_F" "$POST_F" <<'PYEOF'
import sys
def pct(vals, q):
    vs = sorted(vals)
    idx = int(round((len(vs) - 1) * q / 100.0))
    return vs[idx]
gets = [float(x) * 1000 for x in open(sys.argv[1]) if x.strip()]
posts = [float(x) * 1000 for x in open(sys.argv[2]) if x.strip()]
if not gets or not posts:
    print("FAIL 读写混合：无样本"); sys.exit(1)
p50, p90, mx = pct(gets, 50), pct(gets, 90), max(gets)
avg = sum(posts) / len(posts)
print("GET 样本 %d：p50=%.0fms p90=%.0fms max=%.0fms" % (len(gets), p50, p90, mx))
print("POST 样本 %d：avg=%.0fms max=%.0fms" % (len(posts), avg, max(posts)))
bad = []
if p50 > 20: bad.append("GET p50")
if p90 > 50: bad.append("GET p90")
if mx > 150: bad.append("GET max")
if avg > 300: bad.append("POST 平均")
if bad:
    print("FAIL 读写混合：%s 超阈值" % ",".join(bad)); sys.exit(2)
print("PASS 读写混合：GET p50/p90/max 与 POST 平均值均在阈值内")
PYEOF
if [ $? -ne 0 ]; then FAILED=1; fi
kill "$PID_RW" 2>/dev/null || true
# ---- legacy 模式 ----
cp "$WORK/perf.db.json" "$WORK/legacy.db.json"
PID_L=$(start_server "$WORK/legacy.db.json" "$PORT_LEGACY" --legacy); PIDS+=("$PID_L")
wait_ready "$PORT_LEGACY"
L="http://127.0.0.1:$PORT_LEGACY"
curl -s -o /dev/null "$L/posts/2"
echo "== 查询（legacy） =="
q 100 len_gt0 0 "$L/posts?title_like=post%201" "legacy GET /posts?title_like=post%201"
q 200 len 10 "$L/posts?_page=3&_limit=10&_embed=comments" "legacy GET /posts?_page=3&_limit=10&_embed=comments"

# ---- 小库空闲内存 ----
PID_S=$(start_server "$SRC/example/db.json" "$PORT_SMALL"); PIDS+=("$PID_S")
wait_ready "$PORT_SMALL"
sleep 1
echo "== 小库（example/db.json）空闲内存（阈值 30MB） =="
check_mb "小库空闲 VmHWM" "$(hwm_mb "$PID_S")" 30

echo "== 落盘校验：默认模式 3 次 POST 已写入 =="
python3 - "$WORK/default.db.json" <<'PYEOF'
import json, sys
db = json.load(open(sys.argv[1]))
print("posts 总数 =", len(db["posts"]), "（期望 100003）")
sys.exit(0 if len(db["posts"]) == 100003 else 1)
PYEOF

echo "== 用时 $(( $(date +%s) - START ))s，FAILED=$FAILED =="
[ "$FAILED" = 0 ] && echo "PERF-OK"
