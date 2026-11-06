#!/usr/bin/env bash
# 变异检查：每处单独改坏、单独构建、单独跑对应的对照计划，必须出现 FAIL，否则脚本失败。
# 在源码副本上改，不碰工作区。只用 127.0.0.1:3065-3066。需要先执行 scripts/setup-ref.sh。
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT"
REF="${REF_DIR:-$ROOT/.ref}"
WORK="${WORK_DIR:-$ROOT/.work}/mutation"
V1="$REF/v1/node_modules/.bin/json-server"
V0="$REF/v0.17/node_modules/.bin/json-server"

rm -rf "$WORK"; mkdir -p "$WORK"
MISSED=0

# mutate <名称> <文件> <sed 表达式> <计划> <参照> <清单> <数据>
mutate() {
  local name=$1 file=$2 expr=$3 plan=$4 ref=$5 reqs=$6 db=$7
  local dir="$WORK/src"
  rm -rf "$dir"; mkdir -p "$dir"
  tar -C "$SRC" --exclude=.git --exclude=.work --exclude=.ref --exclude=dist -cf - . | tar -C "$dir" -xf -
  sed -i "$expr" "$dir/$file"
  if cmp -s "$SRC/$file" "$dir/$file"; then
    echo "MISS  [$name] sed 没有命中 $file"; MISSED=1; return
  fi
  if ! (cd "$dir" && go build -o "$WORK/js-mut" ./cmd/jsonsrv); then
    echo "MISS  [$name] 编译失败"; MISSED=1; return
  fi
  local out="$WORK/out.txt"
  (cd "$SRC" && python3 scripts/compare.py --plan "$plan" --refbin "$ref" --bin "$WORK/js-mut" \
    --requests "scripts/$reqs" --db "scripts/$db" --ref-port 3065 --impl-port 3066 \
    --workdir "$WORK/cmp") > "$out" 2>&1
  local rc=$?
  local summary; summary=$(grep '^SUMMARY' "$out" | tail -1)
  if [ "$rc" -ne 0 ] && grep -q '^FAIL' "$out"; then
    echo "抓到  [$name] 计划 $plan：$summary"
    grep '^FAIL' "$out" | head -2 | cut -c1-90 | sed 's/^/        /'
  else
    echo "MISS  [$name] 计划 $plan 没有发现（退出码 $rc）：$summary"; MISSED=1
  fi
}

Q=internal/query/query.go
F=internal/httpapi/filters.go
N=internal/httpapi/nested.go
H=internal/httpapi/query.go
M=internal/httpapi/middleware.go

mutate "HEAD 把 404 改成 200" "$M" \
  's/^func (b \*bodyLessWriter) Write(p \[\]byte) (int, error) { return len(p), nil }/&\nfunc (b *bodyLessWriter) WriteHeader(c int) { if c == 404 { c = 200 }; b.ResponseWriter.WriteHeader(c) }/' \
  v1 "$V1" requests-v1.txt compare-db.json
mutate "默认模式 _limit 失效" "$H" \
  's/lim, ok := jsParseInt(sp.limit)/lim, ok := jsParseInt(sp.limit); lim = int64(n)/' \
  default017 "$V0" requests-default-017.txt compare-db2.json
mutate "去掉 URL 值的字符串比较" "$Q" \
  's/if jsToString(f) == b.Raw {/if false \&\& jsToString(f) == b.Raw {/' \
  default017b "$V0" requests-default-017b.txt compare-db3.json
mutate "缺失或 null 父字段放行" "$Q" \
  's/return !deepHasOperators(sub)/return true/' \
  default017b "$V0" requests-default-017b.txt compare-db3.json
mutate "CORS 回显改回 *" "$M" \
  's/h.Set("Access-Control-Allow-Origin", origin)/h.Set("Access-Control-Allow-Origin", "*")/' \
  default017 "$V0" requests-default-017.txt compare-db2.json
mutate "嵌套 POST 外键做类型转换" "$N" \
  's/fkVal := jsonx.Value(idParam)/fkVal := query.CoerceParamValue(idParam)/; s/fkVal = a.recordID(res.asArray()\[idx\])/_ = idx/' \
  default017b "$V0" requests-default-017b.txt compare-db3.json
mutate "未知参数不再忽略" "$F" \
  's/if !hasColon \&\& op == "eq" \&\& !query.LegacyHasParam(records, path) {/if false {/' \
  default017b "$V0" requests-default-017b.txt compare-db3.json

rm -rf "$WORK"
if [ "$MISSED" -ne 0 ]; then echo "MUTATION-FAILED"; exit 1; fi
echo "MUTATION-OK"
