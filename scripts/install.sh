#!/bin/sh
# ITH5 客户端安装脚本。
#
#   curl -fsSL https://<服务端地址>/install.sh | sh
#
# 它会自动探测平台、下载对应二进制、**校验 SHA-256**、装到 ~/.local/bin，
# 并把服务端地址写进配置——新人不需要手工设任何环境变量。
set -eu

SERVER="${ITH5_SERVER:-__SERVER__}"
BIN_DIR="${ITH5_BIN_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux ;;
  *) echo "不支持的系统: $(uname -s)。Windows 请从 $SERVER/dist/ 手工下载。" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64)  ARCH=amd64 ;;
  *) echo "不支持的架构: $(uname -m)" >&2; exit 1 ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "正在从 $SERVER 下载 ${OS}-${ARCH} 版本…"
for f in ith5 ith5-hook SHA256SUMS; do
  if ! curl -fsSL "$SERVER/dist/${OS}-${ARCH}/$f" -o "$TMP/$f"; then
    echo "下载 $f 失败。检查网络或 VPN 是否已连接。" >&2
    exit 1
  fi
done

# 校验：ith5-hook 会被写进 Claude Code 配置并在每次工具调用时执行，
# 它被替换掉的后果比普通文件严重得多。
echo "校验完整性…"
if ! ( cd "$TMP" && shasum -a 256 -c SHA256SUMS >/dev/null 2>&1 ); then
  echo "校验和不匹配，安装已中止。请联系管理员。" >&2
  exit 1
fi

mkdir -p "$BIN_DIR"
install -m 0755 "$TMP/ith5" "$TMP/ith5-hook" "$BIN_DIR/"
cp "$TMP/SHA256SUMS" "$BIN_DIR/SHA256SUMS"

# 预建目录 —— 尤其是 skills：Claude Code 会监视技能目录，但若会话启动时
# 它尚不存在，新建后需重启才被监视。赶在用户第一次启动 Claude Code 之前建好，
# 否则首次 sync 会表现为「装了但看不到」。
mkdir -p "$HOME/.claude/skills" "$HOME/.ith5"

echo "✓ 已安装到 $BIN_DIR"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo
     echo "⚠ $BIN_DIR 不在 PATH 中，请加入你的 shell 配置："
     echo "    export PATH=\"\$PATH:$BIN_DIR\"" ;;
esac

echo
echo "下一步："
echo "    ITH5_SERVER=$SERVER ith5 login"
echo "    ith5 sync"
