#!/bin/sh
set -eu

PANEL_REPOSITORY="${PANEL_REPOSITORY:-https://github.com/xmeng-cx/ipv6-socks-panelv3.git}"
PANEL_INSTALL_DIR="${PANEL_INSTALL_DIR:-/opt/ipv6-socks-panelv3}"
XRAY_VERSION="${XRAY_VERSION:-v26.5.9}"

if [ "$(id -u)" -ne 0 ]; then
  echo "错误：请使用 root 执行安装命令。" >&2
  exit 1
fi

if command -v apt-get >/dev/null 2>&1; then
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y python3 curl unzip openssl iproute2 git ca-certificates
elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache python3 curl unzip openssl iproute2 git ca-certificates
else
  echo "错误：仅支持使用 apt 或 apk 的 Linux 系统。" >&2
  exit 1
fi

if [ -d "$PANEL_INSTALL_DIR/.git" ]; then
  echo "正在更新已有项目…"
  git -C "$PANEL_INSTALL_DIR" fetch "$PANEL_REPOSITORY" main
  git -C "$PANEL_INSTALL_DIR" merge --ff-only FETCH_HEAD
elif [ -e "$PANEL_INSTALL_DIR" ] && [ -n "$(find "$PANEL_INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
  echo "错误：$PANEL_INSTALL_DIR 已存在且不是本项目的 Git 目录。" >&2
  exit 1
else
  echo "正在下载项目…"
  git clone "$PANEL_REPOSITORY" "$PANEL_INSTALL_DIR"
fi

case "$(uname -m)" in
  x86_64|amd64) XRAY_ARCH="64" ;;
  aarch64|arm64) XRAY_ARCH="arm64-v8a" ;;
  armv7l|armv7) XRAY_ARCH="arm32-v7a" ;;
  *) echo "错误：不支持的 CPU 架构 $(uname -m)。" >&2; exit 1 ;;
esac

if ! /usr/local/bin/xray version >/dev/null 2>&1; then
  echo "正在安装 Xray $XRAY_VERSION…"
  XRAY_TEMP_DIR="$(mktemp -d)"
  trap 'rm -rf "$XRAY_TEMP_DIR"' EXIT
  curl -fL "https://github.com/XTLS/Xray-core/releases/download/$XRAY_VERSION/Xray-linux-$XRAY_ARCH.zip" -o "$XRAY_TEMP_DIR/xray.zip"
  unzip -q "$XRAY_TEMP_DIR/xray.zip" -d "$XRAY_TEMP_DIR/xray"
  install -m 0755 "$XRAY_TEMP_DIR/xray/xray" /usr/local/bin/xray
  mkdir -p /usr/local/share/xray
  [ ! -f "$XRAY_TEMP_DIR/xray/geoip.dat" ] || install -m 0644 "$XRAY_TEMP_DIR/xray/geoip.dat" /usr/local/share/xray/geoip.dat
  [ ! -f "$XRAY_TEMP_DIR/xray/geosite.dat" ] || install -m 0644 "$XRAY_TEMP_DIR/xray/geosite.dat" /usr/local/share/xray/geosite.dat
  rm -rf "$XRAY_TEMP_DIR"
  trap - EXIT
fi

cd "$PANEL_INSTALL_DIR"
if [ ! -f .env ]; then
  cp .env.example .env
  echo "已创建默认 .env（后台账号：xmeng / 5201314）。"
else
  echo "保留已有 .env 配置。"
fi
mkdir -p data
chmod 700 data
chmod +x app.py

sed "s|__INSTALL_DIR__|$PANEL_INSTALL_DIR|g" ipv6-socks-panel.service > /etc/systemd/system/ipv6-socks-panel.service
systemctl daemon-reload

if command -v docker >/dev/null 2>&1 && docker inspect ipv6-socks-panel >/dev/null 2>&1; then
  echo "正在停止旧 Docker 容器（保留容器与镜像用于回退）…"
  docker stop ipv6-socks-panel >/dev/null
fi

systemctl enable --now ipv6-socks-panel
sleep 2
systemctl --no-pager --full status ipv6-socks-panel || true
echo "部署完成：请打开 http://服务器IP:8080"
echo "默认后台账号：xmeng / 5201314"
