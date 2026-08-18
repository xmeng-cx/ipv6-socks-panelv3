# IPv6 SOCKS5 / Hysteria2 Docker 管理面板 V2

为同一台 Linux/Armbian 主机创建多条 SOCKS5 或 Hysteria2 入口，每条入口通过 Xray `sendThrough` 固定一个独立公网 IPv6。Web 页面和 HTTP API 可以新增、删除、单独更换或批量更换线路。

## 前提条件

- Linux 主机拥有可自由添加地址的公网 IPv6 前缀（通常为 `/64`）。
- 路由器允许前缀内的不同 IPv6 正常访问公网。
- Docker Engine 和 Docker Compose v2。
- 建议仅在可信网络使用：管理页/API 已有密码鉴权，但 HTTP 和 SOCKS5 本身不加密。

## 启动

### 一键部署

已安装 Docker Engine、Docker Compose v2 和 Git 的 Linux/Armbian 服务器可直接执行：

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv2/main/install.sh | sh
```

默认安装到 `/opt/ipv6-socks-panelv2`，自动创建 `.env`、识别 IPv6 网卡/前缀并启动服务。重复执行会快进更新项目并保留已有 `.env` 和 `data` 状态。如需更换安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/xmeng-cx/ipv6-socks-panelv2/main/install.sh | PANEL_INSTALL_DIR=/root/ipv6-socks-panelv2 sh
```

### 手动部署

```bash
cp .env.example .env
nano .env
docker compose up -d --build
docker compose logs -f
```

如果主机无法访问 Docker Hub，可在 `.env` 增加：

```dotenv
GO_IMAGE=docker.m.daocloud.io/library/golang:1.25-alpine
ALPINE_IMAGE=docker.m.daocloud.io/library/alpine:3.22
GOPROXY=https://goproxy.cn,direct
```

打开 `http://服务器局域网IP:8080`。默认创建端口 `20000`–`20009` 共 10 条线路。
后台默认管理员为 `xmeng`，密码为 `5201314`，首次初始化前可通过 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 覆盖。网页登录后使用带签名的 HttpOnly Cookie 保存会话，默认有效期 90 天，不再弹出浏览器 Basic Auth 对话框。

管理员可以在页面添加或删除普通用户。每个用户只能看到、创建、切换和删除自己的线路；用户新建的 SOCKS5/Hysteria2 线路使用该用户自己的用户名和密码，其他用户即使知道端口也无法使用。升级旧版本时，原有线路自动归属管理员 `xmeng`，端口和 IPv6 保持不变。删除普通用户会同步删除并释放其全部线路。

默认不需要填写网卡或 IPv6 前缀。自动探测顺序为：IPv6 默认路由 → 默认路由网卡 → 网卡公网全局地址 → 地址对应的真实 CIDR。`fc00::/7` ULA 会被排除。

同一 `/64` 下由 DHCPv6 下发的 `/128` 地址会自动忽略。运营商前缀在重启后发生变化时，软件会保留原端口，逐条验证并迁移到新前缀，然后再启动 Xray。线路 ID 固定等于端口号，旧状态会在升级后自动迁移。

页面顶部的“网络设置”可直接修改网卡和 IPv6 前缀。保存后系统会验证新地址、迁移所有线路并持久化设置；两项留空可恢复自动识别。

新增线路默认为 SOCKS5，也可选择 Hysteria2。HY2 使用 UDP/QUIC，并固定监听 `0.0.0.0`（所有 IPv4 网卡）；Xray 的监听地址不使用 `0.0.0.0/0` CIDR 写法。HY2 使用线路所有者的密码认证，使用 `HY2_OBFS_PASSWORD` 配置 Salamander 混淆，并自动生成持久化的自签 TLS 证书。页面可直接复制包含 TLS、SNI、ALPN、Salamander 和 FinalMask 参数的 `hysteria2://` 链接。

存在多个公网前缀时，系统会优先选择内核 IPv6 默认路由使用的源地址所在前缀。只有需要强制使用特定网卡或前缀时，才在 `.env` 手动覆盖：

```dotenv
IPV6_INTERFACE=eth0
IPV6_PREFIX=2408:824e:cb01:3363::/64
```

## 常用命令

```bash
# 状态与日志
docker compose ps
docker compose logs --tail=200

# 更新后重建
docker compose up -d --build

# 正常停止（会释放本软件管理的 IPv6，状态仍保留）
docker compose down
```

重启后会重新绑定状态文件中的原 IPv6。软件只会删除 `/data/state.json` 中记录为受管的地址，不会删除 Armbian 原有地址。

## API

```bash
# 登录并保存会话 Cookie
curl -c panel-cookie.txt -H 'Content-Type: application/json' \
  -d '{"username":"xmeng","password":"5201314"}' \
  http://SERVER_IP:8080/api/v1/auth/login

# 健康状态与当前用户的线路列表
curl -b panel-cookie.txt http://SERVER_IP:8080/api/v1/health
curl -b panel-cookie.txt http://SERVER_IP:8080/api/v1/proxies

# 自动端口新增；或指定 {"port":20020}
curl -b panel-cookie.txt -X POST -H 'Content-Type: application/json' \
  -d '{"protocol":"socks5"}' \
  http://SERVER_IP:8080/api/v1/proxies

# 新增 Hysteria2 线路
curl -b panel-cookie.txt -X POST -H 'Content-Type: application/json' \
  -d '{"port":20020,"protocol":"hy2"}' \
  http://SERVER_IP:8080/api/v1/proxies

# 单条更换、删除（线路 ID 就是端口号）
curl -b panel-cookie.txt -X POST http://SERVER_IP:8080/api/v1/proxies/20000/rotate
curl -b panel-cookie.txt -X DELETE http://SERVER_IP:8080/api/v1/proxies/20000

# 修改前缀；传空字符串可恢复自动识别
curl -b panel-cookie.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"interface":"eth0","prefix":"2408:824e:cb07:bfc0::/64"}' \
  http://SERVER_IP:8080/api/v1/network

# 全部更换并查询任务
curl -b panel-cookie.txt -X POST http://SERVER_IP:8080/api/v1/proxies/rotate-all
curl -b panel-cookie.txt http://SERVER_IP:8080/api/v1/jobs/任务ID

# 管理员添加、列出、删除用户
curl -b panel-cookie.txt -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"alice123"}' \
  http://SERVER_IP:8080/api/v1/users
curl -b panel-cookie.txt http://SERVER_IP:8080/api/v1/users
curl -b panel-cookie.txt -X DELETE http://SERVER_IP:8080/api/v1/users/alice
```

将 `SERVER_IP` 替换为目标机器的局域网 IP。

错误统一返回：

```json
{"error":{"code":"proxy_busy","message":"线路正在执行其他操作","details":null}}
```

## 排障

- `no public IPv6 prefix`：确认 `ip -6 addr show dev eth0 scope global` 有公网地址，或手动设置 `IPV6_PREFIX`。
- `egress mismatch`：新增地址虽然写入网卡，但路由器/运营商没有正确路由该地址。
- IP 检测站不可用：更换 `IP_CHECK_URL`，响应必须是纯 IP 或包含 `ip` 字段的 JSON。
- UDP 客户端无法连接：设置 `SOCKS_UDP_ADVERTISE_IP` 为客户端可访问的 Armbian 局域网 IPv4。

## 跨机器部署说明

项目镜像支持 ARM64 和 AMD64。复制或克隆项目到另一台 Linux 主机后，重新从 `.env.example` 创建 `.env`，不要复制旧机器的 `data/state.json`。目标机器还必须满足：拥有可自由添加地址的公网 IPv6 前缀，并且上级路由器或运营商允许该前缀内的随机地址出站；仅有单个通过 DHCPv6 分配的 `/128` 地址时无法创建地址池。
