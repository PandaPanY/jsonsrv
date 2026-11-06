#!/usr/bin/env bash
# 跑全部 8 个对照计划，任一计划失败返回非零。
# 需要 Go、Python 3、Node.js，先执行 scripts/setup-ref.sh 安装参考版。
# 只用 127.0.0.1 上的 3021-3056 端口，测完自动停。
set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REF="${REF_DIR:-$ROOT/.ref}"
WORK="${WORK_DIR:-$ROOT/.work}/compare"
BIN="$WORK/jsonsrv"
OUT="$WORK/out"
V1="$REF/v1/node_modules/.bin/json-server"
V0="$REF/v0.17/node_modules/.bin/json-server"
DB1="$ROOT/scripts/compare-db.json"
DB2="$ROOT/scripts/compare-db2.json"
DB3="$ROOT/scripts/compare-db3.json"
PY="$ROOT/scripts/compare.py"

if [ ! -x "$V1" ] || [ ! -x "$V0" ]; then
  echo "找不到参考版 json-server，先执行 bash scripts/setup-ref.sh" >&2
  exit 2
fi

mkdir -p "$OUT"
cd "$ROOT" || exit 2
go build -o "$BIN" ./cmd/jsonsrv || exit 1

ALLRC=0
runplan() { # runplan <计划> <参照> <清单> <数据> <参照端口> <本实现端口> [参数] [路由文件]
  local p=$1 refbin=$2 req=$3 db=$4 rp=$5 ip=$6 flags=${7:-} routes=${8:-} rc
  local extra=()
  [ -n "$flags" ] && extra+=("--impl-flags=$flags")
  [ -n "$routes" ] && extra+=("--routes=$routes")
  python3 "$PY" --plan "$p" --refbin "$refbin" --bin "$BIN" \
    --requests "scripts/$req" --db "$db" --ref-port "$rp" --impl-port "$ip" \
    --workdir "$WORK/cmp-$p" "${extra[@]+"${extra[@]}"}" >"$OUT/$p.txt" 2>&1
  rc=$?
  echo "$(grep '^SUMMARY' "$OUT/$p.txt" || echo "SUMMARY plan=$p 没有跑完，见 $OUT/$p.txt") rc=$rc"
  if [ "$rc" -ne 0 ]; then
    ALLRC=1
    if grep -q '^SUMMARY' "$OUT/$p.txt"; then
      grep -E -A5 '^FAIL' "$OUT/$p.txt" | head -40
    else
      # 没跑完多半是服务起不来，CI 上拿不到输出文件，直接打印末尾
      tail -25 "$OUT/$p.txt"
    fi
  fi
}

runplan v1 "$V1" requests-v1.txt "$DB1" 3021 3022
runplan 017 "$V0" requests-v0.17.txt "$DB2" 3023 3024 --legacy "$ROOT/scripts/routes-017.json"
runplan v1b "$V1" requests-v1b.txt "$DB3" 3051 3052
runplan default017 "$V0" requests-default-017.txt "$DB2" 3025 3026
runplan default017b "$V0" requests-default-017b.txt "$DB3" 3053 3054
runplan legacy017b "$V0" requests-legacy-017b.txt "$DB3" 3055 3056 --legacy
runplan extrav1 "$V1" requests-extra-v1.txt "$DB2" 3027 3028
runplan extra017 "$V0" requests-extra-017.txt "$DB2" 3029 3030 --legacy
python3 "$PY" --count
exit $ALLRC
