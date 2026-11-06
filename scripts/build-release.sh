#!/usr/bin/env bash
# 交叉编译 5 个发布产物并生成 SHA256SUMS。版本号只来自 cmd/jsonsrv/main.go。
# 用法：bash scripts/build-release.sh [输出目录，默认 dist]
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT/dist}"
cd "$ROOT"

VERSION=$(sed -n 's/^var version = "\(.*\)"$/\1/p' cmd/jsonsrv/main.go)
if [ -z "$VERSION" ]; then
  echo "无法从 cmd/jsonsrv/main.go 读取版本号" >&2
  exit 1
fi
echo "VERSION=$VERSION"

rm -rf "$OUT"
mkdir -p "$OUT"
build() { # build <GOOS> <GOARCH> <文件名>
  echo "build $3"
  GOOS=$1 GOARCH=$2 CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$3" ./cmd/jsonsrv
}
build linux amd64 jsonsrv-linux-amd64
build linux arm64 jsonsrv-linux-arm64
build darwin amd64 jsonsrv-darwin-amd64
build darwin arm64 jsonsrv-darwin-arm64
build windows amd64 jsonsrv-windows-amd64.exe

(cd "$OUT" && sha256sum jsonsrv-* > SHA256SUMS && cat SHA256SUMS)
