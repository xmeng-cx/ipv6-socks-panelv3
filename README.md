# IPv6 SOCKS5 / Hysteria2 Python 管理面板

原生 Python 3 + Xray 实现的多用户 IPv6 代理管理面板，不使用 Docker，也不需要编译。每条 SOCKS5/Hysteria2 入口拥有独立公网 IPv6，支持用户隔离、批量创建、换 IP、Mihomo 订阅与直连设置。

## 功能

- 自动识别 IPv6 默认路由、网卡和公网前缀，也可由管理员修改。
- 线路 ID 等于端口号，新增协议默认 Hysteria2。
- HY2 同时支持 IPv4 与 IPv6 入站，使用 TLS、Salamander 与独立账号密码；多 IPv6 主机会自动固定稳定入口，避免错误源地址回包。
- 默认管理员：`xmeng` / `5201314`，登录 Cookie 有效期 90 天。
- 管理员可添加/删除用户；新用户默认创建 10 条 HY2 线路。
- 只有管理员可新增线路，可选择目标账号、协议和数量。
- 普通用户只能查看、复制、换 IP、删除自己的线路，看不到网卡与 IPv6 前缀。
- 每个账号有独立 Mihomo 订阅链接；配置使用 `mode: rule`，除管理员设置的直连规则外，其他流量默认走“全局代理”。
- 线路复制和订阅跟随面板访问地址：通过 IPv4 打开导出 IPv4，通过 IPv6 打开导出 IPv6，通过域名打开导出域名。
- 管理员“直连设置”支持域名、IPv4、IPv6 和 CIDR，每行一项。
- systemd 开机自启和故障自动重启。

## 系统要求

- Debian、Ubuntu、Armbian 或 Alpine Linux。
- root 权限。
- 主机拥有可自由添加地址的公网 IPv6 前缀（例如 `/48` 或 `/64`）。
- 上级路由器/运营商允许该前缀内的地址正常出站。

## 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/main/install.sh | sh
```

如果当前网络无法直接访问 GitHub Raw，可从加速入口启动；脚本运行后仍会按公网 IP 自动决定后续下载是否继续使用代理：

```bash
curl -fsSL https://gh.xmeng.eu.org/https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/9924f4b7f8c643819be67d7a7724f8b1c561a339/install.sh | sh
```

安装时会提示填写线路 IPv6 前缀，例如 `2001:470:846a::/48`。填写后会自动保存到 `.env`；直接回车留空则自动识别。重复安装升级时，留空不会覆盖已有前缀。

无人值守安装也可以直接指定前缀：

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/main/install.sh | sh -s -- --ipv6-prefix 2001:470:846a::/48
```

安装时可选择开启 HTTPS，输入已解析到服务器的域名后，脚本会通过 `acme.sh` 自动申请 Let's Encrypt 免费证书并配置续期，无需 Nginx。Python 面板直接提供 HTTPS，所有 HY2 入站也共用这张正式证书。也可无交互执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/main/install.sh | sh -s -- --https panel.example.com admin@example.com
```

申请证书前，请确保域名 A/AAAA 记录已指向该服务器，且公网 TCP 80 和 443 端口可访问。

安装脚本会：

1. 检测公网 IP 所属地区；中国大陆 IP 自动通过 `https://gh.xmeng.eu.org/` 下载 GitHub 项目和 Xray，其他地区直连 GitHub。
2. 安装 Python 3、curl、OpenSSL、iproute2、Git 和 unzip。
3. 下载当前 CPU 架构对应的 Xray。
4. 安装项目到 `/opt/ipv6-socks-panelv3`。
5. 创建并启动 `ipv6-socks-panel.service`，同时安装中文管理命令 `ipv6`。
6. 若检测到旧版同名 Docker 容器，则停止容器但保留它用于回退。

安装完成后打开：`http://服务器IP:8080`

自定义安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv3/main/install.sh | PANEL_INSTALL_DIR=/root/ipv6-socks-panelv3 sh
```

## 配置

首次安装会从 `.env.example` 创建 `.env`。常用选项：

```dotenv
WEB_LISTEN=[::]:8080
ADMIN_USERNAME=xmeng
ADMIN_PASSWORD=5201314
INITIAL_PROXIES=10
BASE_PORT=20001
MAX_PROXIES=100
HY2_OBFS_PASSWORD=5201314

# 默认留空自动识别
IPV6_INTERFACE=
IPV6_PREFIX=

# 客户端连接地址；有域名时建议填写
ADVERTISE_HOST=
```

`[::]` 为 IPv4/IPv6 双栈监听：面板可通过两种协议访问，HY2 和 SOCKS 入站也默认使用 `::` 同时接受 IPv4 与 IPv6 连接。域名需要同时配置 A 和 AAAA 记录，才能让客户端通过两种协议访问。

配置修改后执行：

```bash
systemctl restart ipv6-socks-panel
```

## 管理命令

安装完成后直接输入：

```bash
ipv6
```

即可打开中文管理菜单，支持查看状态、启动、停止、重启、更新、查看日志、显示访问地址和修改面板端口。“更新面板”也会自动检测国内 IP 并选择 GitHub 代理。

也支持直接执行子命令：

```bash
ipv6 status
ipv6 restart
ipv6 update
ipv6 logs
ipv6 access
ipv6 port
```

原始 systemd 命令仍然可用：

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

执行 `ipv6` 后选择“更新面板”，或直接执行 `ipv6 update` 即可拉取新版本并重启服务。更新时同样会自动检测国内 IP 并使用 `https://gh.xmeng.eu.org/`。`data/state.json`、TLS 证书和 `.env` 会保留。

如需手动控制代理检测，可设置 `USE_GITHUB_PROXY=1` 强制使用代理、设置 `USE_GITHUB_PROXY=0` 强制直连；`PANEL_COUNTRY_CODE=CN` 可用于无人值守环境明确指定中国大陆地区。

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

系统会自动转换为 `DOMAIN`、`IP-CIDR` 或 `IP-CIDR6` 规则；输入 `*.example.com` 时使用 `DOMAIN-SUFFIX`。

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

# 无需登录：按用户名更换全部线路 IP，等待验证完成后直接返回结果
curl 'https://panel.example.com/api/v1/rotate-ip?username=alice'

# 无需登录：按用户名和端口只更换单条线路；线路 ID 就是端口号
curl 'https://panel.example.com/api/v1/rotate-ip?username=alice&port=20001'
# 等价路径形式：/api/v1/rotate-ip/alice/20001

# 换 IP / 删除线路
curl -b panel-cookie.txt -X POST http://SERVER_IP:8080/api/v1/proxies/20001/rotate
curl -b panel-cookie.txt -X DELETE http://SERVER_IP:8080/api/v1/proxies/20001
```

公开 GET 换 IP 接口是同步接口，不需要登录或 Cookie。它会生成新 IPv6、验证该地址的公网出口，再通过 Xray HandlerService API 只热替换目标线路的 outbound，不重启 Xray，也不中断其他线路。全部成功后返回 HTTP 200；成功线路包含 `verified: true`，且 `verifiedIpv6` 与 `newIpv6` 一致。验证失败时返回 `verified: false` 和具体错误，并恢复旧 IPv6。

## 数据与安全

- 状态：`data/state.json`
- Xray 配置：`data/xray.json`
- HTTPS 开启时，面板和 HY2 共用 `/root/cert/<域名>/fullchain.pem`、`privkey.pem`；未开启时 HY2 使用 `data/tls/hy2.crt`、`data/tls/hy2.key`。
- 订阅链接相当于访问密钥，请勿公开；删除用户后链接立即失效。
- 管理页面默认使用 HTTP；安装时启用 HTTPS 后由 Python 直接监听 443，并由 `acme.sh` 自动续期证书。
