#!/bin/sh
set -eu

PANEL_REPOSITORY="${PANEL_REPOSITORY:-https://github.com/xmeng-cx/ipv6-socks-panelv3.git}"
PANEL_INSTALL_DIR="${PANEL_INSTALL_DIR:-/opt/ipv6-socks-panelv3}"

if ! command -v git >/dev/null 2>&1; then
  echo "错误：未安装 git。" >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo "错误：请先安装 Docker Engine 和 Docker Compose v2。" >&2
  exit 1
fi

if [ -d "$PANEL_INSTALL_DIR/.git" ]; then
  echo "正在更新已有项目…"
  git -C "$PANEL_INSTALL_DIR" pull --ff-only
elif [ -e "$PANEL_INSTALL_DIR" ] && [ -n "$(find "$PANEL_INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
  echo "错误：$PANEL_INSTALL_DIR 已存在且不是本项目的 Git 目录。" >&2
  exit 1
else
  echo "正在下载项目…"
  git clone "$PANEL_REPOSITORY" "$PANEL_INSTALL_DIR"
fi

cd "$PANEL_INSTALL_DIR"
if [ ! -f .env ]; then
  cp .env.example .env
  echo "已创建默认 .env（后台与代理账号：xmeng / 5201314）。"
else
  echo "保留已有 .env 配置。"
fi

docker compose up -d --build
echo "部署完成：请打开 http://服务器IP:8080"
echo "默认后台账号：xmeng / 5201314"
