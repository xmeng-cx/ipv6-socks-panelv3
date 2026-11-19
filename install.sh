#!/bin/sh
set -eu

PANEL_REPOSITORY="${PANEL_REPOSITORY:-https://github.com/xmeng-cx/ipv6-socks-panelv3.git}"
PANEL_REPOSITORY_DIRECT="$PANEL_REPOSITORY"
PANEL_INSTALL_DIR="${PANEL_INSTALL_DIR:-/opt/ipv6-socks-panelv3}"
XRAY_VERSION="${XRAY_VERSION:-v26.5.9}"
GITHUB_PROXY_BASE="${GITHUB_PROXY_BASE:-https://gh.xmeng.eu.org/}"
USE_GITHUB_PROXY="${USE_GITHUB_PROXY:-auto}"
PANEL_COUNTRY_CODE="${PANEL_COUNTRY_CODE:-}"
ENABLE_HTTPS="${ENABLE_HTTPS:-}"
HTTPS_DOMAIN="${HTTPS_DOMAIN:-}"
HTTPS_EMAIL="${HTTPS_EMAIL:-}"
INSTALL_IPV6_PREFIX="${PANEL_IPV6_PREFIX:-}"
NON_INTERACTIVE=0

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
    --non-interactive) NON_INTERACTIVE=1; shift ;;
    --ipv6-prefix)
      [ "$#" -ge 2 ] || { echo "错误：--ipv6-prefix 后必须提供 IPv6 前缀。" >&2; exit 1; }
      INSTALL_IPV6_PREFIX="$2"
      shift 2
      ;;
    *) echo "错误：未知参数 $1" >&2; exit 1 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "错误：请使用 root 执行安装命令。" >&2
  exit 1
fi

HAS_TTY=0
if [ -t 0 ] || [ -t 1 ] || [ -t 2 ]; then
  HAS_TTY=1
fi
if [ "$NON_INTERACTIVE" = "1" ]; then
  HAS_TTY=0
fi

if [ -z "$INSTALL_IPV6_PREFIX" ] && [ "$HAS_TTY" = "1" ]; then
  printf '请输入线路 IPv6 前缀（例如 2001:470:846a::/48，留空自动识别）: ' >/dev/tty
  IFS= read -r INSTALL_IPV6_PREFIX </dev/tty || true
fi

if [ -z "$ENABLE_HTTPS" ]; then
  ENABLE_HTTPS=0
  if [ "$HAS_TTY" = "1" ]; then
    printf '是否启用 HTTPS 并自动申请 Let\047s Encrypt 免费证书？[y/N]: ' >/dev/tty
    IFS= read -r HTTPS_ANSWER </dev/tty || true
    case "$HTTPS_ANSWER" in y|Y|yes|YES) ENABLE_HTTPS=1 ;; esac
  fi
fi

if [ "$ENABLE_HTTPS" = "1" ]; then
  if [ -z "$HTTPS_DOMAIN" ] && [ "$HAS_TTY" = "1" ]; then
    printf '请输入已解析到本服务器的域名: ' >/dev/tty
    IFS= read -r HTTPS_DOMAIN </dev/tty
  fi
  if [ -z "$HTTPS_EMAIL" ] && [ "$HAS_TTY" = "1" ]; then
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

detect_country_code() {
  COUNTRY_CODE="$(printf '%s' "$PANEL_COUNTRY_CODE" | tr '[:lower:]' '[:upper:]' | tr -cd 'A-Z')"
  if [ -z "$COUNTRY_CODE" ]; then
    COUNTRY_RESPONSE="$(curl -4 -fsS --max-time 5 https://api.country.is 2>/dev/null || true)"
    COUNTRY_CODE="$(printf '%s' "$COUNTRY_RESPONSE" | sed -n 's/.*"country"[[:space:]]*:[[:space:]]*"\([A-Za-z][A-Za-z]\)".*/\1/p' | tr '[:lower:]' '[:upper:]')"
  fi
  if [ -z "$COUNTRY_CODE" ]; then
    COUNTRY_CODE="$(curl -4 -fsS --max-time 5 https://ipapi.co/country/ 2>/dev/null | tr '[:lower:]' '[:upper:]' | tr -cd 'A-Z' || true)"
  fi
  if [ -z "$COUNTRY_CODE" ]; then
    COUNTRY_RESPONSE="$(curl -4 -fsS --max-time 5 https://myip.ipip.net 2>/dev/null || true)"
    if printf '%s' "$COUNTRY_RESPONSE" | grep -q '中国'; then COUNTRY_CODE=CN; fi
  fi
  printf '%s' "$COUNTRY_CODE"
}

case "$GITHUB_PROXY_BASE" in */) ;; *) GITHUB_PROXY_BASE="$GITHUB_PROXY_BASE/" ;; esac
COUNTRY_CODE="$(detect_country_code)"
case "$USE_GITHUB_PROXY" in
  1|true|TRUE|yes|YES) USE_GITHUB_PROXY=1 ;;
  0|false|FALSE|no|NO) USE_GITHUB_PROXY=0 ;;
  auto|AUTO|'') if [ "$COUNTRY_CODE" = "CN" ]; then USE_GITHUB_PROXY=1; else USE_GITHUB_PROXY=0; fi ;;
  *) echo "错误：USE_GITHUB_PROXY 仅支持 auto、1 或 0。" >&2; exit 1 ;;
esac

github_download_url() {
  case "$1" in
    https://github.com/*|https://raw.githubusercontent.com/*)
      if [ "$USE_GITHUB_PROXY" = "1" ]; then printf '%s%s' "$GITHUB_PROXY_BASE" "$1"; else printf '%s' "$1"; fi
      ;;
    *) printf '%s' "$1" ;;
  esac
}

if [ "$USE_GITHUB_PROXY" = "1" ]; then
  echo "检测到中国大陆 IP，GitHub 下载将使用代理：$GITHUB_PROXY_BASE"
else
  if [ -n "$COUNTRY_CODE" ]; then
    echo "检测到当前地区：$COUNTRY_CODE，GitHub 下载使用直连。"
  else
    echo "未能识别 IP 地区，GitHub 下载使用直连。"
  fi
fi
PANEL_REPOSITORY="$(github_download_url "$PANEL_REPOSITORY")"

git_fetch_main() {
  REPOSITORY_DIR="$1"
  if git -C "$REPOSITORY_DIR" -c http.version=HTTP/1.1 fetch "$PANEL_REPOSITORY" main; then
    return 0
  fi
  if [ "$PANEL_REPOSITORY" != "$PANEL_REPOSITORY_DIRECT" ]; then
    echo "GitHub 代理请求失败，正在回退到直连……" >&2
    git -C "$REPOSITORY_DIR" -c http.version=HTTP/1.1 fetch "$PANEL_REPOSITORY_DIRECT" main
    return $?
  fi
  return 1
}

git_clone_panel() {
  if git -c http.version=HTTP/1.1 clone "$PANEL_REPOSITORY" "$PANEL_INSTALL_DIR"; then
    return 0
  fi
  if [ "$PANEL_REPOSITORY" != "$PANEL_REPOSITORY_DIRECT" ]; then
    echo "GitHub 代理请求失败，正在回退到直连……" >&2
    git -c http.version=HTTP/1.1 clone "$PANEL_REPOSITORY_DIRECT" "$PANEL_INSTALL_DIR"
    return $?
  fi
  return 1
}

if [ -n "$INSTALL_IPV6_PREFIX" ]; then
  if ! INSTALL_IPV6_PREFIX="$(python3 -c 'import ipaddress,sys; n=ipaddress.ip_network(sys.argv[1], strict=False); assert n.version == 6 and n.prefixlen < 128; print(n)' "$INSTALL_IPV6_PREFIX" 2>/dev/null)"; then
    echo "错误：IPv6 前缀无效，请填写类似 2001:470:846a::/48 的网段。" >&2
    exit 1
  fi
fi

if [ -d "$PANEL_INSTALL_DIR/.git" ]; then
  echo "正在更新已有项目…"
  if [ -n "$(git -C "$PANEL_INSTALL_DIR" status --porcelain --untracked-files=normal)" ]; then
    STASH_LABEL="ipv6-panel-auto-backup-$(date +%Y%m%d-%H%M%S)"
    git -C "$PANEL_INSTALL_DIR" stash push --include-untracked -m "$STASH_LABEL"
    echo "已将旧代码改动保存到 Git stash：$STASH_LABEL"
  fi
  git_fetch_main "$PANEL_INSTALL_DIR"
  git -C "$PANEL_INSTALL_DIR" merge --ff-only FETCH_HEAD
elif [ -e "$PANEL_INSTALL_DIR" ] && [ -n "$(find "$PANEL_INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
  echo "错误：$PANEL_INSTALL_DIR 已存在且不是本项目的 Git 目录。" >&2
  exit 1
else
  echo "正在下载项目…"
  git_clone_panel
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
  XRAY_DOWNLOAD_URL="$(github_download_url "https://github.com/XTLS/Xray-core/releases/download/$XRAY_VERSION/Xray-linux-$XRAY_ARCH.zip")"
  curl -fL "$XRAY_DOWNLOAD_URL" -o "$XRAY_TEMP_DIR/xray.zip"
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

set_env_value() {
  ENV_KEY="$1"
  ENV_VALUE="$2"
  if grep -q "^${ENV_KEY}=" "$PANEL_INSTALL_DIR/.env"; then
    sed -i "s|^${ENV_KEY}=.*|${ENV_KEY}=${ENV_VALUE}|" "$PANEL_INSTALL_DIR/.env"
  else
    printf '%s=%s\n' "$ENV_KEY" "$ENV_VALUE" >> "$PANEL_INSTALL_DIR/.env"
  fi
}

if [ -n "$INSTALL_IPV6_PREFIX" ]; then
  set_env_value IPV6_PREFIX "$INSTALL_IPV6_PREFIX"
  echo "已配置线路 IPv6 前缀：$INSTALL_IPV6_PREFIX"
else
  echo "未指定线路 IPv6 前缀，将由面板自动识别（已有配置保持不变）。"
fi
mkdir -p data
chmod 700 data

sed "s|__INSTALL_DIR__|$PANEL_INSTALL_DIR|g" ipv6-socks-panel.service > /etc/systemd/system/ipv6-socks-panel.service
sed "s|__INSTALL_DIR__|$PANEL_INSTALL_DIR|g" ipv6 > /usr/local/bin/ipv6
chmod 0755 /usr/local/bin/ipv6
systemctl daemon-reload

if command -v docker >/dev/null 2>&1 && docker inspect ipv6-socks-panel >/dev/null 2>&1; then
  echo "正在停止旧 Docker 容器（保留容器与镜像用于回退）…"
  docker stop ipv6-socks-panel >/dev/null
fi

systemctl enable ipv6-socks-panel
systemctl restart ipv6-socks-panel

if [ "$ENABLE_HTTPS" = "1" ]; then
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
  echo "部署完成：请输入 ipv6 查看当前访问地址。"
fi
echo "默认后台账号：xmeng / 5201314"
echo "管理命令：ipv6"
