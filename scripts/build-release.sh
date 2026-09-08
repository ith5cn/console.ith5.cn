#!/usr/bin/env bash
# 构建各平台的客户端二进制与校验清单。
#
# 产物放在 dist/，由 ith5-server 直接托管：新人只需要一条 curl 命令，
# 不需要访问任何额外的下载站点。
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${1:-dev}"
OUT=dist
rm -rf "$OUT" && mkdir -p "$OUT"

PLATFORMS=(
  "darwin/arm64"
  "darwin/amd64"
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
)

for p in "${PLATFORMS[@]}"; do
  GOOS="${p%/*}"; GOARCH="${p#*/}"
  suffix=""; [ "$GOOS" = "windows" ] && suffix=".exe"
  dir="$OUT/${GOOS}-${GOARCH}"
  mkdir -p "$dir"
  echo "构建 ${GOOS}/${GOARCH}"
  for cmd in ith5 ith5-hook; do
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
      -o "$dir/${cmd}${suffix}" "./cmd/${cmd}"
  done
  # 每个平台目录内一份清单：doctor 会拿它核对 hook 二进制
  ( cd "$dir" && shasum -a 256 ith5* > SHA256SUMS )
done

# 顶层总清单，供 install.sh 校验下载结果
( cd "$OUT" && find . -type f -name 'ith5*' | sort | xargs shasum -a 256 > SHA256SUMS )

echo
echo "产物:"
find "$OUT" -type f | sort | sed 's/^/  /'
