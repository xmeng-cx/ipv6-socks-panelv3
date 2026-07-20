# IPv6 SOCKS5 Docker 管理面板

为同一台 Linux/Armbian 主机创建多条 SOCKS5 入口，每条入口通过 Xray `sendThrough` 固定一个独立公网 IPv6。Web 页面和 HTTP API 可以新增、删除、单独更换或批量更换线路。

## 前提条件

- Linux 主机拥有可自由添加地址的公网 IPv6 前缀（通常为 `/64`）。
- 路由器允许前缀内的不同 IPv6 正常访问公网。
- Docker Engine 和 Docker Compose v2。
- 仅限可信局域网：管理页面/API 没有鉴权，SOCKS5 本身也不加密。

## 启动

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

默认不需要填写网卡或 IPv6 前缀。自动探测顺序为：IPv6 默认路由 → 默认路由网卡 → 网卡公网全局地址 → 地址对应的真实 CIDR。`fc00::/7` ULA 会被排除。

同一 `/64` 下由 DHCPv6 下发的 `/128` 地址会自动忽略。运营商前缀在重启后发生变化时，软件会保留原端口和线路 ID，逐条验证并迁移到新前缀，然后再启动 Xray。

只有设备存在多个互不包含的公网前缀、或需要使用非默认路由网卡时，才在 `.env` 手动覆盖：

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
# 健康状态与线路列表
curl http://SERVER_IP:8080/api/v1/health
curl http://SERVER_IP:8080/api/v1/proxies

# 自动端口新增；或指定 {"port":20020}
curl -X POST -H 'Content-Type: application/json' -d '{}' \
  http://SERVER_IP:8080/api/v1/proxies

# 单条更换、删除
curl -X POST http://SERVER_IP:8080/api/v1/proxies/线路ID/rotate
curl -X DELETE http://SERVER_IP:8080/api/v1/proxies/线路ID

# 全部更换并查询任务
curl -X POST http://SERVER_IP:8080/api/v1/proxies/rotate-all
curl http://SERVER_IP:8080/api/v1/jobs/任务ID
```

将 `SERVER_IP` 替换为目标机器的局域网 IP。

错误统一返回：

```json
{"error":{"code":"proxy_busy","message":"线路正在执行其他操作","details":null}}
```

## 排障

- `no public IPv6 prefix`：确认 `ip -6 addr show dev eth0 scope global` 有公网地址，或手动设置 `IPV6_PREFIX`。
- `multiple public IPv6 prefixes`：必须手动指定要使用的前缀。
- `egress mismatch`：新增地址虽然写入网卡，但路由器/运营商没有正确路由该地址。
- IP 检测站不可用：更换 `IP_CHECK_URL`，响应必须是纯 IP 或包含 `ip` 字段的 JSON。
- UDP 客户端无法连接：设置 `SOCKS_UDP_ADVERTISE_IP` 为客户端可访问的 Armbian 局域网 IPv4。

## 跨机器部署说明

项目镜像支持 ARM64 和 AMD64。复制或克隆项目到另一台 Linux 主机后，重新从 `.env.example` 创建 `.env`，不要复制旧机器的 `data/state.json`。目标机器还必须满足：拥有可自由添加地址的公网 IPv6 前缀，并且上级路由器或运营商允许该前缀内的随机地址出站；仅有单个通过 DHCPv6 分配的 `/128` 地址时无法创建地址池。
