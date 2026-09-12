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
  if [ "$ENABLE_HTTPS" = "1" ]; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y nginx certbot
  fi
elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache python3 curl unzip openssl iproute2 git ca-certificates
  if [ "$ENABLE_HTTPS" = "1" ]; then apk add --no-cache nginx certbot; fi
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
  set_env_value WEB_LISTEN 127.0.0.1:8080
  set_env_value ADVERTISE_HOST "$HTTPS_DOMAIN"
  systemctl restart ipv6-socks-panel

  mkdir -p /var/www/html
  if [ -d /etc/nginx/sites-available ]; then
    NGINX_CONFIG=/etc/nginx/sites-available/ipv6-socks-panel
    NGINX_ENABLED=/etc/nginx/sites-enabled/ipv6-socks-panel
  else
    NGINX_CONFIG=/etc/nginx/http.d/ipv6-socks-panel.conf
    NGINX_ENABLED=
  fi
  cat > "$NGINX_CONFIG" <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name $HTTPS_DOMAIN;
    location ^~ /.well-known/acme-challenge/ { root /var/www/html; }
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto http;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    }
}
EOF
  [ -z "$NGINX_ENABLED" ] || ln -sfn "$NGINX_CONFIG" "$NGINX_ENABLED"
  nginx -t
  systemctl enable --now nginx
  systemctl reload nginx

  if [ -n "$HTTPS_EMAIL" ]; then
    certbot certonly --webroot -w /var/www/html -d "$HTTPS_DOMAIN" --non-interactive --agree-tos --email "$HTTPS_EMAIL" --keep-until-expiring
  else
    certbot certonly --webroot -w /var/www/html -d "$HTTPS_DOMAIN" --non-interactive --agree-tos --register-unsafely-without-email --keep-until-expiring
  fi

  cat > "$NGINX_CONFIG" <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name $HTTPS_DOMAIN;
    location ^~ /.well-known/acme-challenge/ { root /var/www/html; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name $HTTPS_DOMAIN;
    ssl_certificate /etc/letsencrypt/live/$HTTPS_DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$HTTPS_DOMAIN/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    }
}
EOF
  nginx -t
  systemctl reload nginx
  mkdir -p /etc/letsencrypt/renewal-hooks/deploy
  printf '#!/bin/sh\nsystemctl reload nginx\n' > /etc/letsencrypt/renewal-hooks/deploy/ipv6-socks-panel-nginx.sh
  chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/ipv6-socks-panel-nginx.sh
  systemctl enable --now certbot.timer 2>/dev/null || true
fi

sleep 2
systemctl --no-pager --full status ipv6-socks-panel || true
if [ "$ENABLE_HTTPS" = "1" ]; then
  echo "部署完成：请打开 https://$HTTPS_DOMAIN"
else
  echo "部署完成：请打开 http://服务器IP:8080"
fi
echo "默认后台账号：xmeng / 5201314"
