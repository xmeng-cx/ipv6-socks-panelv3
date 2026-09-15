#!/bin/sh
set -eu

PANEL_REPOSITORY="${PANEL_REPOSITORY:-https://github.com/xmeng-cx/ipv6-socks-panelv3.git}"
PANEL_INSTALL_DIR="${PANEL_INSTALL_DIR:-/opt/ipv6-socks-panelv3}"
XRAY_VERSION="${XRAY_VERSION:-v26.5.9}"
ENABLE_HTTPS="${ENABLE_HTTPS:-}"
HTTPS_DOMAIN="${HTTPS_DOMAIN:-}"
HTTPS_EMAIL="${HTTPS_EMAIL:-}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --https)
      ENABLE_HTTPS=1
      [ "$#" -ge 2 ] || { echo "错误：--https 后必须提供域名。" >&2; exit 1; }
      HTTPS_DOMAIN="$2"
      shift 2
      if [ "$#" -gt 0 ] && [ "${1#--}" = "$1" ]; then HTTPS_EMAIL="$1"; shift; fi
      ;;
    --no-https) ENABLE_HTTPS=0; shift ;;
    *) echo "错误：未知参数 $1" >&2; exit 1 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "错误：请使用 root 执行安装命令。" >&2
  exit 1
fi

if [ -z "$ENABLE_HTTPS" ]; then
  ENABLE_HTTPS=0
  if [ -r /dev/tty ]; then
    printf '是否启用 HTTPS 并自动申请 Let\047s Encrypt 免费证书？[y/N]: ' >/dev/tty
    IFS= read -r HTTPS_ANSWER </dev/tty || true
    case "$HTTPS_ANSWER" in y|Y|yes|YES) ENABLE_HTTPS=1 ;; esac
  fi
fi

if [ "$ENABLE_HTTPS" = "1" ]; then
  if [ -z "$HTTPS_DOMAIN" ] && [ -r /dev/tty ]; then
    printf '请输入已解析到本服务器的域名: ' >/dev/tty
    IFS= read -r HTTPS_DOMAIN </dev/tty
  fi
  if [ -z "$HTTPS_EMAIL" ] && [ -r /dev/tty ]; then
    printf '请输入证书到期提醒邮箱（可留空）: ' >/dev/tty
    IFS= read -r HTTPS_EMAIL </dev/tty || true
  fi
  printf '%s' "$HTTPS_DOMAIN" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$' || { echo "错误：HTTPS 域名无效。" >&2; exit 1; }
  case "$HTTPS_DOMAIN" in *.*) ;; *) echo "错误：Let's Encrypt 需要可公网验证的完整域名。" >&2; exit 1 ;; esac
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
  if [ -n "$(git -C "$PANEL_INSTALL_DIR" status --porcelain --untracked-files=normal)" ]; then
    STASH_LABEL="ipv6-panel-auto-backup-$(date +%Y%m%d-%H%M%S)"
    git -C "$PANEL_INSTALL_DIR" stash push --include-untracked -m "$STASH_LABEL"
    echo "已将旧代码改动保存到 Git stash：$STASH_LABEL"
  fi
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

sed "s|__INSTALL_DIR__|$PANEL_INSTALL_DIR|g" ipv6-socks-panel.service > /etc/systemd/system/ipv6-socks-panel.service
systemctl daemon-reload

if command -v docker >/dev/null 2>&1 && docker inspect ipv6-socks-panel >/dev/null 2>&1; then
  echo "正在停止旧 Docker 容器（保留容器与镜像用于回退）…"
  docker stop ipv6-socks-panel >/dev/null
fi

systemctl enable --now ipv6-socks-panel

if [ "$ENABLE_HTTPS" = "1" ]; then
  set_env_value() {
    ENV_KEY="$1"
    ENV_VALUE="$2"
    if grep -q "^${ENV_KEY}=" "$PANEL_INSTALL_DIR/.env"; then
      sed -i "s|^${ENV_KEY}=.*|${ENV_KEY}=${ENV_VALUE}|" "$PANEL_INSTALL_DIR/.env"
    else
      printf '%s=%s\n' "$ENV_KEY" "$ENV_VALUE" >> "$PANEL_INSTALL_DIR/.env"
    fi
  }
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet nginx 2>/dev/null; then
    echo "正在停止并禁用旧 Nginx，改由 Python 直接提供 HTTPS…"
    systemctl disable --now nginx
  fi
  if ss -lnt | awk '$4 ~ /:80$/ { found=1 } END { exit !found }'; then
    echo "错误：80 端口仍被其他程序占用：" >&2
    ss -lntp | awk '$4 ~ /:80$/ { print }' >&2
    exit 1
  fi
  ACME_HOME=/root/.acme.sh
  if [ ! -x "$ACME_HOME/acme.sh" ]; then
    echo "正在安装 acme.sh…"
    if [ -n "$HTTPS_EMAIL" ]; then
      curl -fsSL https://get.acme.sh | sh -s email="$HTTPS_EMAIL"
    else
      curl -fsSL https://get.acme.sh | sh
    fi
  fi
  "$ACME_HOME/acme.sh" --set-default-ca --server letsencrypt
  echo "正在使用 80 端口申请 Let's Encrypt 证书…"
  "$ACME_HOME/acme.sh" --issue --standalone --httpport 80 -d "$HTTPS_DOMAIN" --keylength ec-256
  CERT_DIR="/root/cert/$HTTPS_DOMAIN"
  mkdir -p "$CERT_DIR"
  "$ACME_HOME/acme.sh" --install-cert -d "$HTTPS_DOMAIN" --ecc \
    --key-file "$CERT_DIR/privkey.pem" \
    --fullchain-file "$CERT_DIR/fullchain.pem" \
    --reloadcmd "systemctl restart ipv6-socks-panel"
  chmod 600 "$CERT_DIR/privkey.pem"
  chmod 644 "$CERT_DIR/fullchain.pem"
  "$ACME_HOME/acme.sh" --upgrade --auto-upgrade

  set_env_value WEB_LISTEN "[::]:443"
  set_env_value ADVERTISE_HOST "$HTTPS_DOMAIN"
  set_env_value TLS_CERT_FILE "$CERT_DIR/fullchain.pem"
  set_env_value TLS_KEY_FILE "$CERT_DIR/privkey.pem"
  systemctl restart ipv6-socks-panel
fi

sleep 2
systemctl --no-pager --full status ipv6-socks-panel || true
if [ "$ENABLE_HTTPS" = "1" ]; then
  echo "部署完成：请打开 https://$HTTPS_DOMAIN"
else
  echo "部署完成：请打开 http://服务器IP:8080"
fi
echo "默认后台账号：xmeng / 5201314"
