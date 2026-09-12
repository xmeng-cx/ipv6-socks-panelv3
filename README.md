# IPv6 SOCKS5 / Hysteria2 Python 管理面板

原生 Python 3 + Xray 实现的多用户 IPv6 代理管理面板，不使用 Docker，也不需要编译。每条 SOCKS5/Hysteria2 入口拥有独立公网 IPv6，支持用户隔离、批量创建、换 IP、Mihomo 订阅与直连设置。

## 功能

- 自动识别 IPv6 默认路由、网卡和公网前缀，也可由管理员修改。
- 线路 ID 等于端口号，新增协议默认 Hysteria2。
- HY2 监听 `0.0.0.0`，使用 TLS、Salamander 与独立账号密码。
- 默认管理员：`xmeng` / `5201314`，登录 Cookie 有效期 90 天。
- 管理员可添加/删除用户；新用户默认创建 10 条 HY2 线路。
- 只有管理员可新增线路，可选择目标账号、协议和数量。
- 普通用户只能查看、复制、换 IP、删除自己的线路，看不到网卡与 IPv6 前缀。
- 每个账号有独立 Mihomo 订阅链接；配置使用 `mode: rule`，除管理员设置的直连规则外，其他流量默认走“全局代理”。
- 管理员“直连设置”支持域名、IPv4、IPv6 和 CIDR，每行一项。
- systemd 开机自启和故障自动重启。

## 系统要求

- Debian、Ubuntu、Armbian 或 Alpine Linux。
- root 权限。
- 主机拥有可自由添加地址的公网 IPv6 前缀（通常为 `/64`）。
- 上级路由器/运营商允许该前缀内的地址正常出站。

## 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/main/install.sh | sh
```

安装脚本会：

1. 安装 Python 3、curl、OpenSSL、iproute2、Git 和 unzip。
2. 下载当前 CPU 架构对应的 Xray。
3. 安装项目到 `/opt/ipv6-socks-panelv3`。
4. 创建并启动 `ipv6-socks-panel.service`。
5. 若检测到旧版同名 Docker 容器，则停止容器但保留它用于回退。

安装完成后打开：`http://服务器IP:8080`

自定义安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/main/install.sh | PANEL_INSTALL_DIR=/root/ipv6-socks-panelv3 sh
```

## 配置

首次安装会从 `.env.example` 创建 `.env`。常用选项：

```dotenv
WEB_LISTEN=0.0.0.0:8080
ADMIN_USERNAME=xmeng
ADMIN_PASSWORD=5201314
INITIAL_PROXIES=10
BASE_PORT=20000
MAX_PROXIES=100
HY2_OBFS_PASSWORD=5201314

# 默认留空自动识别
IPV6_INTERFACE=
IPV6_PREFIX=

# 客户端连接地址；有域名时建议填写
ADVERTISE_HOST=
```

配置修改后执行：

```bash
systemctl restart ipv6-socks-panel
```

## 管理命令

```bash
systemctl status ipv6-socks-panel
journalctl -u ipv6-socks-panel -f
systemctl restart ipv6-socks-panel
systemctl stop ipv6-socks-panel
```

Python 服务直接运行：

```bash
cd /opt/ipv6-socks-panelv3
python3 app.py
```

## 升级

重复执行一键安装命令即可拉取新版本并重启服务。`data/state.json`、TLS 证书和 `.env` 会保留。

从 Docker 版原目录迁移时，也可以指定旧项目目录安装；Python 版兼容已有 `data/state.json`：

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/main/install.sh | PANEL_INSTALL_DIR=/root/ipv6-socks-panel sh
```

建议迁移前备份：

```bash
cp /root/ipv6-socks-panel/data/state.json /root/ipv6-socks-panel/data/state.backup.json
```

## 直连设置格式

页面中只填写原始域名、IP 或 CIDR，不写 Mihomo 规则前缀，每行一项：

```text
example.com
101.35.154.10/32
192.168.0.0/16
2001:db8::/32
```

系统会自动转换为 `DOMAIN-SUFFIX`、`IP-CIDR` 或 `IP-CIDR6` 规则。

## API 示例

```bash
# 登录并保存 Cookie
curl -c panel-cookie.txt -H 'Content-Type: application/json' \
  -d '{"username":"xmeng","password":"5201314"}' \
  http://SERVER_IP:8080/api/v1/auth/login

# 管理员为 alice 创建 10 条 HY2
curl -b panel-cookie.txt -X POST -H 'Content-Type: application/json' \
  -d '{"owner":"alice","protocol":"hy2","count":10}' \
  http://SERVER_IP:8080/api/v1/proxies

# 查看当前账号线路
curl -b panel-cookie.txt http://SERVER_IP:8080/api/v1/proxies

# 换 IP / 删除线路
curl -b panel-cookie.txt -X POST http://SERVER_IP:8080/api/v1/proxies/20000/rotate
curl -b panel-cookie.txt -X DELETE http://SERVER_IP:8080/api/v1/proxies/20000
```

## 数据与安全

- 状态：`data/state.json`
- Xray 配置：`data/xray.json`
- HY2 证书：`data/tls/hy2.crt`、`data/tls/hy2.key`
- 订阅链接相当于访问密钥，请勿公开；删除用户后链接立即失效。
- 管理页面默认使用 HTTP，公网使用时建议通过反向代理配置 HTTPS，并限制管理端口访问来源。
