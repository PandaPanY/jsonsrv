#!/usr/bin/env bash
# 安装对照用的 json-server（1.0.0-beta.15 和 0.17.4），各自独立的 node_modules，不全局安装。
# 默认装到仓库内的 .ref/（已被 git 忽略），可用 REF_DIR 指定别处。已安装则只打印版本。
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REF="${REF_DIR:-$ROOT/.ref}"

install_ref() { # install_ref <目录名> <版本>
  local dir="$REF/$1"
  mkdir -p "$dir"
  if [ ! -x "$dir/node_modules/.bin/json-server" ]; then
    (cd "$dir" && npm init -y >/dev/null && npm install --no-audit --no-fund "json-server@$2")
  fi
  echo "json-server $1: $(node -p "require('$dir/node_modules/json-server/package.json').version")"
}

install_ref v1 1.0.0-beta.15
install_ref v0.17 0.17.4
