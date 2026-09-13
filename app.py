#!/usr/bin/env python3
import base64
import contextlib
import hashlib
import hmac
import http.cookies
import http.server
import ipaddress
import json
import os
import re
import secrets
import shutil
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path(__file__).resolve().parent
WEB_ROOT = ROOT / "web"
STATE_VERSION = 4
SESSION_COOKIE = "ipv6_panel_session"
SESSION_LIFETIME = 90 * 24 * 3600
PASSWORD_ITERATIONS = 120000
USERNAME_RE = re.compile(r"^[A-Za-z0-9._-]{3,32}$")
DEFAULT_DIRECT_RULES = [
    "101.35.154.10/32",
    "64.118.133.57/32",
    "64.118.129.251/32",
    "114.66.40.113/32",
    "127.0.0.0/8",
    "10.0.0.0/8",
    "172.16.0.0/12",
    "192.168.0.0/16",
    "100.64.0.0/10",
    "169.254.0.0/16",
    "::1/128",
    "fc00::/7",
    "fe80::/10",
    "www.meiguodizhi.com",
    "lyj.xmeng.eu.org",
    "api.xuerabbit.cn",
    "dcloud.net.cn",
]


def utc_now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def env_bool(name, default):
    value = os.getenv(name, "").strip().lower()
    return default if not value else value in ("1", "true", "yes", "on")


def env_seconds(name, default):
    value = os.getenv(name, "").strip().lower()
    if not value:
        return default
    if value.endswith("ms"):
        return max(1, int(value[:-2]) / 1000)
    if value.endswith("s"):
        value = value[:-1]
    return int(value)


class Config:
    def __init__(self):
        self.web_listen = os.getenv("WEB_LISTEN", "0.0.0.0:8080")
        self.data_dir = Path(os.getenv("DATA_DIR", str(ROOT / "data")))
        self.xray_binary = os.getenv("XRAY_BINARY", "/usr/local/bin/xray")
        self.socks_listen = os.getenv("SOCKS_LISTEN", "0.0.0.0")
        self.socks_udp = env_bool("SOCKS_UDP", True)
        self.udp_advertise_ip = os.getenv("SOCKS_UDP_ADVERTISE_IP", "").strip()
        self.advertise_host = os.getenv("ADVERTISE_HOST", "").strip()
        self.tls_cert_file = os.getenv("TLS_CERT_FILE", "").strip()
        self.tls_key_file = os.getenv("TLS_KEY_FILE", "").strip()
        self.admin_username = os.getenv("ADMIN_USERNAME", "xmeng")
        self.admin_password = os.getenv("ADMIN_PASSWORD", "5201314")
        self.hy2_obfs_password = os.getenv("HY2_OBFS_PASSWORD", "5201314")
        self.initial_proxies = int(os.getenv("INITIAL_PROXIES", "10"))
        self.base_port = int(os.getenv("BASE_PORT", "20001"))
        self.max_proxies = int(os.getenv("MAX_PROXIES", "100"))
        self.ipv6_interface = os.getenv("IPV6_INTERFACE", "").strip()
        self.ipv6_prefix = os.getenv("IPV6_PREFIX", "").strip()
        self.ip_check_url = os.getenv("IP_CHECK_URL", "https://api6.ipify.org")
        self.ip_check_timeout = env_seconds("IP_CHECK_TIMEOUT", 10)
        self.dad_timeout = env_seconds("DAD_TIMEOUT", 8)
        if not 1 <= self.max_proxies <= 100:
            raise ValueError("MAX_PROXIES must be between 1 and 100")
        if not 0 <= self.initial_proxies <= self.max_proxies:
            raise ValueError("INITIAL_PROXIES must be between 0 and MAX_PROXIES")
        if bool(self.tls_cert_file) != bool(self.tls_key_file):
            raise ValueError("TLS_CERT_FILE and TLS_KEY_FILE must be configured together")

    @property
    def tls_enabled(self):
        return bool(self.tls_cert_file and self.tls_key_file)


def derive_password(password, salt):
    block = hashlib.sha256(salt + password.encode()).digest()
    for _ in range(1, PASSWORD_ITERATIONS):
        block = hashlib.sha256(block + salt + password.encode()).digest()
    return block.hex()


def new_user(username, password, role="user"):
    username = username.strip()
    if not USERNAME_RE.fullmatch(username):
        raise ValueError("用户名只能包含字母、数字、点、横线和下划线，长度 3–32 位")
    if not 6 <= len(password) <= 128:
        raise ValueError("密码长度必须为 6–128 位")
    salt = secrets.token_bytes(16)
    return {
        "username": username, "role": role, "passwordSalt": salt.hex(),
        "passwordHash": derive_password(password, salt), "proxyPassword": password,
        "subscriptionToken": secrets.token_urlsafe(32), "createdAt": utc_now(),
    }


class PanelError(Exception):
    def __init__(self, message, status=500, code="operation_failed"):
        super().__init__(message)
        self.status, self.code = status, code


class Panel:
    def __init__(self, cfg):
        self.cfg = cfg
        self.lock = threading.RLock()
        self.jobs = {}
        self.xray = None
        self.stopping = threading.Event()
        self.network = {}
        self.prefix = None
        self.state_path = cfg.data_dir / "state.json"
        self.state = self._load_state()

    def _load_state(self):
        self.cfg.data_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        if self.state_path.exists():
            state = json.loads(self.state_path.read_text("utf-8"))
            version = int(state.get("version", 1))
            if version < 1 or version > STATE_VERSION:
                raise RuntimeError("unsupported state version %d" % version)
        else:
            version = 0
            state = {"version": STATE_VERSION, "proxies": [], "pending": {}, "users": []}
        state.setdefault("proxies", [])
        state.setdefault("pending", {})
        state.setdefault("users", [])
        if version < 4:
            state["directRules"] = normalize_direct_rules(DEFAULT_DIRECT_RULES + state.get("directRules", []))
        else:
            state.setdefault("directRules", list(DEFAULT_DIRECT_RULES))
        state["version"] = STATE_VERSION
        if not state.get("sessionSecret"):
            state["sessionSecret"] = secrets.token_urlsafe(32)
        if not state["users"]:
            state["users"].append(new_user(self.cfg.admin_username, self.cfg.admin_password, "admin"))
        for user in state["users"]:
            if not user.get("subscriptionToken"):
                user["subscriptionToken"] = secrets.token_urlsafe(32)
        admin = self.find_user(self.cfg.admin_username, state)
        if not admin:
            raise RuntimeError("configured administrator is missing")
        for proxy in state["proxies"]:
            proxy["id"] = str(proxy.get("port"))
            proxy.setdefault("protocol", "socks5")
            proxy.setdefault("owner", admin["username"])
            owner = self.find_user(proxy["owner"], state)
            if not owner:
                raise RuntimeError("proxy owner is missing: %s" % proxy["owner"])
            proxy.setdefault("username", owner["username"])
            proxy.setdefault("password", owner["proxyPassword"])
        self._save_state_value(state)
        return state

    @staticmethod
    def find_user(username, state):
        return next((u for u in state.get("users", []) if hmac.compare_digest(u["username"], username)), None)

    def save(self):
        self._save_state_value(self.state)

    def _save_state_value(self, state):
        self.cfg.data_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        fd, temp_name = tempfile.mkstemp(prefix="state-", suffix=".tmp", dir=self.cfg.data_dir)
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as stream:
                json.dump(state, stream, ensure_ascii=False, indent=2)
                stream.write("\n")
            os.chmod(temp_name, 0o600)
            os.replace(temp_name, self.state_path)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(temp_name)

    @staticmethod
    def run(args, timeout=20, check=True):
        return subprocess.run(args, text=True, capture_output=True, timeout=timeout, check=check)

    def discover_network(self, iface_override="", prefix_override=""):
        iface = iface_override.strip()
        preferred_source = ""
        routes = json.loads(self.run(["ip", "-j", "-6", "route", "show", "default"]).stdout or "[]")
        if not iface:
            if not routes:
                raise RuntimeError("未找到 IPv6 默认路由")
            route = sorted(routes, key=lambda item: item.get("metric", 0))[0]
            iface, preferred_source = route.get("dev", ""), route.get("prefsrc", route.get("src", ""))
        if not iface:
            raise RuntimeError("无法识别 IPv6 网卡")
        addresses = json.loads(self.run(["ip", "-j", "addr", "show", "dev", iface]).stdout or "[]")
        addr_info = addresses[0].get("addr_info", []) if addresses else []
        candidates = []
        ipv4 = ""
        for item in addr_info:
            local = item.get("local", "")
            if item.get("family") == "inet" and not ipv4:
                ipv4 = local
            if item.get("family") != "inet6" or item.get("scope") != "global":
                continue
            ip = ipaddress.ip_address(local)
            if ip.is_private or ip.is_link_local or ip.is_loopback or ip.is_multicast:
                continue
            plen = int(item.get("prefixlen", 128))
            if plen < 128:
                candidates.append(ipaddress.ip_network("%s/%d" % (local, plen), strict=False))
        if prefix_override.strip():
            prefix = ipaddress.ip_network(prefix_override.strip(), strict=False)
            if prefix.version != 6 or prefix.prefixlen >= 128:
                raise ValueError("IPv6 前缀无效")
        else:
            unique = sorted(set(candidates), key=lambda n: (n.prefixlen, str(n)))
            if not unique:
                raise RuntimeError("网卡 %s 没有可用的公网 IPv6 前缀" % iface)
            preferred = ipaddress.ip_address(preferred_source) if preferred_source else None
            prefix = next((n for n in unique if preferred and preferred in n), unique[0])
        return {"interface": iface, "prefix": str(prefix), "ipv4": ipv4}, prefix

    def start(self):
        iface = self.state.get("ipv6Interface") or self.cfg.ipv6_interface
        prefix = self.state.get("ipv6Prefix") or self.cfg.ipv6_prefix
        self.network, self.prefix = self.discover_network(iface, prefix)
        for proxy in self.state["proxies"]:
            ip = ipaddress.ip_address(proxy["ipv6"])
            if ip not in self.prefix:
                proxy["ipv6"] = str(self.random_ip())
            self.add_address(proxy["ipv6"])
            proxy["status"] = "healthy"
        missing = self.cfg.initial_proxies - len(self.state["proxies"])
        if missing > 0:
            self._create_proxies(self.cfg.admin_username, "hy2", missing)
        self.save()
        self.restart_xray()
        threading.Thread(target=self.monitor_xray, daemon=True).start()

    def stop(self):
        self.stopping.set()
        with self.lock:
            self.stop_xray()

    def monitor_xray(self):
        while not self.stopping.wait(5):
            with self.lock:
                if self.xray is None or self.xray.poll() is not None:
                    try:
                        self.restart_xray()
                    except Exception as error:
                        print("Xray 自动重启失败：%s" % error, file=sys.stderr, flush=True)

    def random_ip(self):
        used = {ipaddress.ip_address(p["ipv6"]) for p in self.state["proxies"]}
        for _ in range(1000):
            bits = 128 - self.prefix.prefixlen
            ip = ipaddress.ip_address(int(self.prefix.network_address) | secrets.randbits(bits))
            if ip not in used and ip != self.prefix.network_address:
                return ip
        raise RuntimeError("无法生成未使用的 IPv6")

    def add_address(self, ip):
        cidr = "%s/%d" % (ip, self.prefix.prefixlen)
        result = self.run(["ip", "-6", "addr", "add", cidr, "dev", self.network["interface"], "noprefixroute"], check=False)
        deadline = time.time() + self.cfg.dad_timeout
        while time.time() < deadline:
            data = json.loads(self.run(["ip", "-j", "-6", "addr", "show", "dev", self.network["interface"]]).stdout or "[]")
            entries = data[0].get("addr_info", []) if data else []
            item = next((a for a in entries if a.get("local") == str(ip)), None)
            if result.returncode and not item:
                raise RuntimeError(result.stderr.strip())
            if item:
                flags = {str(flag).lower() for flag in item.get("flags", [])}
                tentative = bool(item.get("tentative")) or "tentative" in flags
                dad_failed = bool(item.get("dadfailed")) or "dadfailed" in flags
                if dad_failed:
                    raise RuntimeError("IPv6 %s 重复地址检测失败" % ip)
                if not tentative:
                    return
            time.sleep(.15)
        raise RuntimeError("等待 IPv6 %s 就绪超时" % ip)

    def delete_address(self, ip):
        cidr = "%s/%d" % (ip, self.prefix.prefixlen)
        self.run(["ip", "-6", "addr", "del", cidr, "dev", self.network["interface"]], check=False)

    def check_egress(self, ip):
        result = self.run(["curl", "-6", "--interface", str(ip), "-fsS", "--max-time", str(self.cfg.ip_check_timeout), self.cfg.ip_check_url], timeout=self.cfg.ip_check_timeout + 3, check=False)
        if result.returncode:
            raise RuntimeError("IPv6 出站验证失败: %s" % result.stderr.strip())
        observed = result.stdout.strip()
        try:
            payload = json.loads(observed)
            observed = payload.get("ip", observed)
        except ValueError:
            pass
        if ipaddress.ip_address(observed.strip()) != ipaddress.ip_address(ip):
            raise RuntimeError("IPv6 出站不一致，预期 %s，实际 %s" % (ip, observed))

    def ensure_certificate(self):
        if self.cfg.tls_enabled:
            cert, key = Path(self.cfg.tls_cert_file), Path(self.cfg.tls_key_file)
            if not cert.is_file() or not key.is_file():
                raise RuntimeError("已配置的 HTTPS 证书或私钥不存在")
            return cert, key
        tls_dir = self.cfg.data_dir / "tls"
        cert, key = tls_dir / "hy2.crt", tls_dir / "hy2.key"
        if cert.exists() and key.exists():
            return cert, key
        tls_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        host = self.cfg.advertise_host or self.network.get("ipv4") or "localhost"
        san = "IP:%s" % host if self._is_ip(host) else "DNS:%s" % host
        self.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "3650", "-subj", "/CN=" + host, "-addext", "subjectAltName=" + san, "-keyout", str(key), "-out", str(cert)], timeout=30)
        os.chmod(key, 0o600)
        return cert, key

    @staticmethod
    def _is_ip(value):
        try:
            ipaddress.ip_address(value)
            return True
        except ValueError:
            return False

    def xray_config(self):
        cert, key = self.ensure_certificate()
        inbounds = [{"tag": "api-in", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door", "settings": {"address": "127.0.0.1"}}]
        outbounds, rules = [], [{"type": "field", "ruleTag": "api-rule", "inboundTag": ["api-in"], "outboundTag": "api"}]
        for proxy in self.state["proxies"]:
            tag = str(proxy["id"])
            if proxy["protocol"] == "hy2":
                inbound = {"tag": "socks-" + tag, "listen": "0.0.0.0", "port": proxy["port"], "protocol": "hysteria", "settings": {"version": 2, "users": [{"auth": proxy["password"], "email": proxy["username"]}]}, "streamSettings": {"network": "hysteria", "security": "tls", "tlsSettings": {"alpn": ["h3"], "minVersion": "1.3", "maxVersion": "1.3", "certificates": [{"certificateFile": str(cert), "keyFile": str(key)}]}, "hysteriaSettings": {"version": 2, "auth": proxy["password"], "udpIdleTimeout": 60}, "finalmask": {"udp": [{"type": "salamander", "settings": {"password": self.cfg.hy2_obfs_password}}]}}}
            else:
                settings = {"auth": "password", "udp": self.cfg.socks_udp, "users": [{"user": proxy["username"], "pass": proxy["password"]}]}
                udp_ip = self.cfg.udp_advertise_ip or self.network.get("ipv4")
                if self.cfg.socks_udp and udp_ip:
                    settings["ip"] = udp_ip
                inbound = {"tag": "socks-" + tag, "listen": self.cfg.socks_listen, "port": proxy["port"], "protocol": "socks", "settings": settings}
            inbounds.append(inbound)
            outbounds.append({"tag": "egress-" + tag, "protocol": "freedom", "sendThrough": proxy["ipv6"], "settings": {"domainStrategy": "UseIPv6"}})
            rules.append({"type": "field", "ruleTag": "route-" + tag, "inboundTag": ["socks-" + tag], "outboundTag": "egress-" + tag})
        return {"log": {"loglevel": "warning"}, "api": {"tag": "api", "services": ["HandlerService", "RoutingService"]}, "inbounds": inbounds, "outbounds": outbounds, "routing": {"domainStrategy": "AsIs", "rules": rules}}

    def stop_xray(self):
        process, self.xray = self.xray, None
        if process and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()

    def restart_xray(self):
        config_path = self.cfg.data_dir / "xray.json"
        config_path.write_text(json.dumps(self.xray_config(), ensure_ascii=False, indent=2) + "\n", "utf-8")
        test = self.run([self.cfg.xray_binary, "run", "-test", "-c", str(config_path)], check=False)
        if test.returncode:
            raise RuntimeError("Xray 配置验证失败: " + test.stderr.strip())
        self.stop_xray()
        self.xray = subprocess.Popen([self.cfg.xray_binary, "run", "-c", str(config_path)])
        time.sleep(.35)
        if self.xray.poll() is not None:
            raise RuntimeError("Xray 启动失败")

    def authenticate(self, username, password):
        with self.lock:
            user = self.find_user(username.strip(), self.state)
            if not user:
                return None
            try:
                actual = derive_password(password, bytes.fromhex(user["passwordSalt"]))
            except ValueError:
                return None
            return user if hmac.compare_digest(actual, user["passwordHash"]) else None

    def list_for_user(self, username):
        with self.lock:
            return sorted([dict(p) for p in self.state["proxies"] if p["owner"] == username], key=lambda p: p["port"])

    def users_view(self):
        with self.lock:
            counts = {}
            for proxy in self.state["proxies"]:
                counts[proxy["owner"]] = counts.get(proxy["owner"], 0) + 1
            return [{"username": u["username"], "role": u["role"], "proxyCount": counts.get(u["username"], 0), "createdAt": u["createdAt"], "subscriptionToken": u["subscriptionToken"]} for u in self.state["users"]]

    def allocate_port(self, requested=None):
        used = {p["port"] for p in self.state["proxies"]}
        if requested is not None:
            if requested in used or not 1 <= requested <= 65535:
                raise PanelError("端口不可用", 409, "port_unavailable")
            return requested
        for port in range(self.cfg.base_port, self.cfg.base_port + self.cfg.max_proxies):
            if port not in used:
                return port
        raise PanelError("已达到线路数量上限", 409, "proxy_limit")

    def create_proxies(self, owner, protocol="hy2", count=1, requested_port=None):
        with self.lock:
            return self._create_proxies(owner, protocol, count, requested_port)

    def _create_proxies(self, owner, protocol="hy2", count=1, requested_port=None):
        if not self.find_user(owner, self.state):
            raise PanelError("目标账号不存在", 404, "not_found")
        protocol = protocol.strip().lower() or "hy2"
        if protocol in ("hysteria", "hysteria2"):
            protocol = "hy2"
        if protocol in ("socks", "sk5"):
            protocol = "socks5"
        if protocol not in ("hy2", "socks5"):
            raise PanelError("不支持的协议", 400, "invalid_protocol")
        if not 1 <= count <= self.cfg.max_proxies or len(self.state["proxies"]) + count > self.cfg.max_proxies:
            raise PanelError("已达到线路数量上限", 409, "proxy_limit")
        if count > 1 and requested_port is not None:
            raise PanelError("批量创建线路时不能手动指定端口", 400, "invalid_port")
        user = self.find_user(owner, self.state)
        created = []
        activated = []
        had_xray = bool(self.xray and self.xray.poll() is None)
        try:
            for _ in range(count):
                port = self.allocate_port(requested_port)
                ip = str(self.random_ip())
                self.add_address(ip)
                activated.append(ip)
                self.check_egress(ip)
                proxy = {"id": str(port), "port": port, "protocol": protocol, "ipv6": ip, "status": "healthy", "createdAt": utc_now(), "owner": owner, "username": owner, "password": user["proxyPassword"]}
                self.state["proxies"].append(proxy)
                created.append(proxy)
            self.restart_xray()
            self.save()
            return [dict(p) for p in created]
        except Exception:
            for proxy in created:
                self.state["proxies"].remove(proxy)
            for ip in activated:
                self.delete_address(ip)
            if had_xray:
                with contextlib.suppress(Exception):
                    self.restart_xray()
            raise

    def delete_proxy(self, username, proxy_id):
        with self.lock:
            proxy = next((p for p in self.state["proxies"] if p["id"] == proxy_id and p["owner"] == username), None)
            if not proxy:
                raise PanelError("线路不存在", 404, "not_found")
            self.state["proxies"].remove(proxy)
            try:
                self.restart_xray()
                self.delete_address(proxy["ipv6"])
                self.save()
            except Exception:
                self.state["proxies"].append(proxy)
                self.restart_xray()
                raise

    def rotate_proxy(self, username, proxy_id):
        with self.lock:
            proxy = next((p for p in self.state["proxies"] if p["id"] == proxy_id and p["owner"] == username), None)
            if not proxy:
                raise PanelError("线路不存在", 404, "not_found")
            old = proxy["ipv6"]
            new = str(self.random_ip())
            self.add_address(new)
            try:
                self.check_egress(new)
                proxy["ipv6"] = new
                proxy["lastRotatedAt"] = utc_now()
                self.restart_xray()
                self.delete_address(old)
                self.save()
                return dict(proxy)
            except Exception:
                proxy["ipv6"] = old
                self.delete_address(new)
                with contextlib.suppress(Exception):
                    self.restart_xray()
                raise

    def add_user(self, username, password):
        with self.lock:
            if self.find_user(username.strip(), self.state):
                raise PanelError("用户名已存在", 409, "user_exists")
            user = new_user(username, password)
            self.state["users"].append(user)
            try:
                self._create_proxies(user["username"], "hy2", 10)
                self.save()
                return user
            except Exception:
                self.state["users"].remove(user)
                self.save()
                raise

    def delete_user(self, username):
        with self.lock:
            user = self.find_user(username, self.state)
            if not user:
                raise PanelError("用户不存在", 404, "not_found")
            if user["role"] == "admin":
                raise PanelError("不能删除管理员", 403, "forbidden")
            proxies = [p for p in self.state["proxies"] if p["owner"] == username]
            self.state["proxies"] = [p for p in self.state["proxies"] if p["owner"] != username]
            self.state["users"].remove(user)
            self.restart_xray()
            for proxy in proxies:
                self.delete_address(proxy["ipv6"])
            self.save()

    def update_network(self, iface, prefix):
        with self.lock:
            info, network = self.discover_network(iface, prefix)
            old_info, old_prefix = self.network, self.prefix
            old_ips = [p["ipv6"] for p in self.state["proxies"]]
            new_ips = []
            self.network, self.prefix = info, network
            try:
                for proxy in self.state["proxies"]:
                    ip = str(self.random_ip())
                    self.add_address(ip)
                    self.check_egress(ip)
                    new_ips.append(ip)
                    proxy["ipv6"] = ip
                    proxy["lastRotatedAt"] = utc_now()
                self.restart_xray()
                for old_ip in old_ips:
                    self.run(["ip", "-6", "addr", "del", "%s/%d" % (old_ip, old_prefix.prefixlen), "dev", old_info["interface"]], check=False)
                self.state["ipv6Interface"] = iface.strip()
                self.state["ipv6Prefix"] = prefix.strip()
                self.save()
                return info
            except Exception:
                for ip in new_ips:
                    self.delete_address(ip)
                for proxy, old_ip in zip(self.state["proxies"], old_ips):
                    proxy["ipv6"] = old_ip
                self.network, self.prefix = old_info, old_prefix
                with contextlib.suppress(Exception):
                    self.restart_xray()
                raise


def b64url(data):
    return base64.urlsafe_b64encode(data).decode().rstrip("=")


def sign_session(secret, username, expires):
    payload = "%s|%d" % (username, expires)
    signature = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).digest()
    return b64url(payload.encode()) + "." + b64url(signature)


def parse_session(secret, token):
    try:
        encoded, signature = token.split(".", 1)
        payload = base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4))
        actual = base64.urlsafe_b64decode(signature + "=" * (-len(signature) % 4))
        expected = hmac.new(secret.encode(), payload, hashlib.sha256).digest()
        username, expires = payload.decode().rsplit("|", 1)
        return username if hmac.compare_digest(actual, expected) and time.time() < int(expires) else None
    except (ValueError, UnicodeDecodeError):
        return None


def normalize_direct_rules(values):
    result = []
    for raw in values:
        value = str(raw).strip().lower()
        if not value:
            continue
        if "," in value:
            raise PanelError("直连项不能包含逗号", 400, "invalid_direct_rule")
        cleaned = value.removeprefix("*.").removeprefix(".")
        try:
            ipaddress.ip_network(value, strict=False)
        except ValueError:
            if "." not in cleaned or not all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", part) for part in cleaned.split(".")):
                raise PanelError("%s 不是有效的域名、IP 或 CIDR" % value, 400, "invalid_direct_rule")
        if value not in result:
            result.append(value)
    return result


def mihomo_direct_rule(value):
    try:
        network = ipaddress.ip_network(value, strict=False)
        kind = "IP-CIDR6" if network.version == 6 else "IP-CIDR"
        return "%s,%s,DIRECT,no-resolve" % (kind, network)
    except ValueError:
        if value.startswith(("*.", ".")):
            return "DOMAIN-SUFFIX,%s,DIRECT" % value.removeprefix("*.").removeprefix(".")
        return "DOMAIN,%s,DIRECT" % value


def yaml_quote(value):
    return json.dumps(str(value), ensure_ascii=False)


def mihomo_config(proxies, server, obfs_password, direct_rules, tls_verified=False):
    lines = [
        "# Mihomo Android Root 完整配置",
        "# 由 IPv6 Socks Panel 自动生成",
        "# 保存路径：/data/adb/mihomo/config.yaml",
        "",
        "mixed-port: 7890",
        "allow-lan: true",
        'bind-address: "*"',
        "mode: rule",
        "log-level: info",
        "ipv6: true",
        "unified-delay: true",
        "tcp-concurrent: true",
        "external-controller: 0.0.0.0:9090",
        'secret: "xmeng"',
        "external-ui: /data/adb/mihomo/ui",
        "external-ui-name: zashboard",
        "profile:",
        "  store-selected: true",
        "  store-fake-ip: true",
        "sniffer:",
        "  enable: true",
        "  force-dns-mapping: true",
        "  parse-pure-ip: true",
        "  sniff:",
        "    HTTP:",
        "      ports:",
        "        - 80",
        "        - 8080-8880",
        "      override-destination: true",
        "    TLS:",
        "      ports:",
        "        - 443",
        "        - 8443",
        "      override-destination: true",
        "    QUIC:",
        "      ports:",
        "        - 443",
        "        - 8443",
        "      override-destination: true",
        "tun:",
        "  enable: true",
        "  device: mihomo",
        "  stack: mixed",
        "  dns-hijack:",
        '    - "any:53"',
        '    - "tcp://any:53"',
        "  auto-route: true",
        "  auto-redirect: true",
        "  auto-detect-interface: true",
        "  strict-route: true",
        "  mtu: 1400",
        "dns:",
        "  enable: true",
        "  listen: 0.0.0.0:1053",
        "  ipv6: false",
        "  enhanced-mode: fake-ip",
        "  fake-ip-range: 198.18.0.1/16",
        "  use-hosts: true",
        "  use-system-hosts: true",
        "  fake-ip-filter:",
        '    - "*.lan"',
        '    - "*.local"',
        '    - "localhost"',
        '    - "+.msftconnecttest.com"',
        '    - "+.msftncsi.com"',
        "  default-nameserver:",
        "    - 223.5.5.5",
        "    - 119.29.29.29",
        "  nameserver:",
        "    - 223.5.5.5",
        "    - 119.29.29.29",
        "  proxy-server-nameserver:",
        "    - 223.5.5.5",
        "    - 119.29.29.29",
        "proxies:",
    ]
    names = []
    if not proxies:
        lines[-1] += " []"
    for proxy in proxies:
        name = ("US-HY" if proxy["protocol"] == "hy2" else "US-SK5") + str(proxy["port"])
        names.append(name)
        lines += ["  - name: " + yaml_quote(name), "    type: " + ("hysteria2" if proxy["protocol"] == "hy2" else "socks5"), "    server: " + yaml_quote(server), "    port: %d" % proxy["port"]]
        if proxy["protocol"] == "hy2":
            lines += ["    password: " + yaml_quote(proxy["password"]), "    sni: " + yaml_quote(server), "    skip-cert-verify: " + ("false" if tls_verified else "true"), "    obfs: salamander", "    obfs-password: " + yaml_quote(obfs_password), "    alpn:", "      - h3", "      - h2", "      - http/1.1", "    udp: true"]
        else:
            lines += ["    username: " + yaml_quote(proxy["username"]), "    password: " + yaml_quote(proxy["password"]), "    udp: true"]
    group_names = names or ["DIRECT"]
    lines += ["proxy-groups:", '  - name: "全局代理"', "    type: select", "    proxies:"] + ["      - " + yaml_quote(n) for n in group_names]
    if names:
        lines.append('      - "自动选择"')
    lines += ['  - name: "自动选择"', "    type: url-test", "    proxies:"] + ["      - " + yaml_quote(n) for n in group_names]
    lines += ['    url: "http://[2001:67c:1898:11::46]/"', "    interval: 300", "    tolerance: 100", "    lazy: true"]
    built_in_direct_rules = [
        "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
        "100.64.0.0/10", "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
    ]
    combined_direct_rules = []
    for rule in list(direct_rules) + [server] + built_in_direct_rules:
        rendered = mihomo_direct_rule(rule)
        if rendered not in combined_direct_rules:
            combined_direct_rules.append(rendered)
    lines += ["rules:"] + ["  - " + rule for rule in combined_direct_rules]
    lines.append("  - MATCH,全局代理")
    return "\n".join(lines) + "\n"


class Handler(http.server.BaseHTTPRequestHandler):
    server_version = "IPv6PanelPython/3.1"

    @property
    def panel(self):
        return self.server.panel

    def log_message(self, fmt, *args):
        sys.stderr.write("%s - %s\n" % (self.address_string(), fmt % args))

    def security_headers(self):
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("X-Frame-Options", "DENY")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:")

    def json_response(self, status, value):
        body = json.dumps(value, ensure_ascii=False).encode()
        self.send_response(status)
        self.security_headers()
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def error_response(self, error):
        if not isinstance(error, PanelError):
            error = PanelError(str(error))
        self.json_response(error.status, {"error": {"code": error.code, "message": str(error), "details": None}})

    def body_json(self):
        length = min(int(self.headers.get("Content-Length", "0")), 1024 * 1024)
        return json.loads(self.rfile.read(length) or b"{}")

    def identity(self):
        cookie = http.cookies.SimpleCookie(self.headers.get("Cookie", ""))
        morsel = cookie.get(SESSION_COOKIE)
        if not morsel:
            return None
        username = parse_session(self.panel.state["sessionSecret"], morsel.value)
        return self.panel.find_user(username, self.panel.state) if username else None

    def require_user(self):
        user = self.identity()
        if user:
            return user
        if self.path.startswith("/api/"):
            self.error_response(PanelError("登录已过期，请重新登录", 401, "unauthorized"))
        else:
            self.send_response(303)
            self.send_header("Location", "/login.html")
            self.end_headers()
        return None

    def require_admin(self):
        user = self.require_user()
        if user and user["role"] != "admin":
            self.error_response(PanelError("仅管理员可以执行此操作", 403, "forbidden"))
            return None
        return user

    def subscription_url(self, token):
        forwarded = self.headers.get("X-Forwarded-Proto", "").split(",", 1)[0].strip().lower()
        scheme = "https" if forwarded == "https" or isinstance(self.connection, ssl.SSLSocket) else "http"
        return "%s://%s/sub/%s" % (scheme, self.headers.get("Host", "localhost"), token)

    def start_rotate_job(self, owner, proxy_id=None, wait=False):
        proxies = self.panel.list_for_user(owner)
        if proxy_id:
            proxies = [proxy for proxy in proxies if proxy["id"] == str(proxy_id)]
            if not proxies:
                raise PanelError("该账号下不存在指定端口的线路", 404, "proxy_not_found")
        job_id = secrets.token_hex(12)
        job = {"id": job_id, "status": "running", "total": len(proxies), "completed": 0, "succeeded": 0, "failed": 0, "createdAt": utc_now(), "items": {}, "owner": owner}
        self.panel.jobs[job_id] = job
        def rotate_all():
            for proxy in proxies:
                item = {"proxyId": proxy["id"], "status": "running", "oldIpv6": proxy["ipv6"]}
                job["items"][proxy["id"]] = item
                try:
                    updated = self.panel.rotate_proxy(owner, proxy["id"])
                    item.update({"status": "success", "newIpv6": updated["ipv6"], "verified": True, "verifiedIpv6": updated["ipv6"]})
                    job["succeeded"] += 1
                except Exception as exc:
                    item.update({"status": "failed", "verified": False, "error": str(exc)})
                    job["failed"] += 1
                job["completed"] += 1
            job.update({"status": "completed", "completedAt": utc_now()})
        if wait:
            rotate_all()
        else:
            threading.Thread(target=rotate_all, daemon=True).start()
        return {k: v for k, v in job.items() if k != "owner"}

    def do_GET(self):
        parsed = urllib.parse.urlsplit(self.path)
        path = parsed.path
        try:
            if path == "/healthz":
                self.send_response(204 if self.panel.xray and self.panel.xray.poll() is None else 503)
                self.end_headers()
                return
            if path.startswith("/sub/"):
                token = path[len("/sub/"):]
                user = next((u for u in self.panel.state["users"] if hmac.compare_digest(u["subscriptionToken"], token)), None)
                if not user:
                    self.send_error(404)
                    return
                host = self.panel.cfg.advertise_host or self.headers.get("Host", "localhost").split(":", 1)[0]
                body = mihomo_config(self.panel.list_for_user(user["username"]), host, self.panel.cfg.hy2_obfs_password, self.panel.state["directRules"], self.panel.cfg.tls_enabled).encode()
                self.send_response(200); self.security_headers(); self.send_header("Content-Type", "text/yaml; charset=utf-8"); self.send_header("Cache-Control", "no-store"); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body); return
            if path == "/api/v1/auth/me":
                user = self.require_user()
                if user: self.json_response(200, {"Username": user["username"], "Role": user["role"]})
                return
            if path == "/api/v1/health":
                user = self.require_user()
                if not user: return
                proxies = self.panel.list_for_user(user["username"])
                xray_running = bool(self.panel.xray and self.panel.xray.poll() is None)
                result = {"status": "healthy" if xray_running else "unhealthy", "message": "ok" if xray_running else "Xray 未运行", "xrayRunning": xray_running, "proxyCount": len(proxies), "initialProxies": self.panel.cfg.initial_proxies, "maxProxies": self.panel.cfg.max_proxies, "basePort": self.panel.cfg.base_port, "username": user["username"], "role": user["role"], "isAdmin": user["role"] == "admin", "advertiseHost": self.panel.cfg.advertise_host, "udp": self.panel.cfg.socks_udp, "hy2ObfsPassword": self.panel.cfg.hy2_obfs_password, "tlsVerified": self.panel.cfg.tls_enabled, "subscriptionUrl": self.subscription_url(user["subscriptionToken"])}
                if user["role"] == "admin": result.update({"network": self.panel.network, "totalProxyCount": len(self.panel.state["proxies"]), "directRules": self.panel.state["directRules"]})
                self.json_response(200 if xray_running else 503, result); return
            if path == "/api/v1/proxies":
                user = self.require_user()
                if user: self.json_response(200, {"proxies": self.panel.list_for_user(user["username"])})
                return
            if path == "/api/v1/users":
                if not self.require_admin(): return
                users = self.panel.users_view()
                for user in users: user["subscriptionUrl"] = self.subscription_url(user.pop("subscriptionToken"))
                self.json_response(200, {"users": users}); return
            if path == "/api/v1/rotate-ip" or path.startswith("/api/v1/rotate-ip/"):
                query = urllib.parse.parse_qs(parsed.query)
                owner = query.get("username", [""])[0].strip()
                proxy_id = query.get("port", query.get("id", [""]))[0].strip()
                if path.startswith("/api/v1/rotate-ip/"):
                    parts = [urllib.parse.unquote(part).strip() for part in path[len("/api/v1/rotate-ip/"):].split("/") if part]
                    owner = parts[0] if parts else ""
                    proxy_id = parts[1] if len(parts) > 1 else ""
                if not owner:
                    raise PanelError("请提供 username", 400, "username_required")
                if not self.panel.find_user(owner, self.panel.state):
                    raise PanelError("目标账号不存在", 404, "not_found")
                self.json_response(200, self.start_rotate_job(owner, proxy_id or None, wait=True)); return
            if path.startswith("/api/v1/jobs/"):
                user = self.require_user()
                job = self.panel.jobs.get(path.rsplit("/", 1)[-1]) if user else None
                if not job or (user["role"] != "admin" and job["owner"] != user["username"]): raise PanelError("任务不存在", 404, "job_not_found")
                self.json_response(200, {k: v for k, v in job.items() if k != "owner"}); return
            self.serve_static(path)
        except Exception as error:
            self.error_response(error)

    def do_POST(self):
        path = urllib.parse.urlsplit(self.path).path
        try:
            if path == "/api/v1/auth/login":
                data = self.body_json(); user = self.panel.authenticate(data.get("username", ""), data.get("password", ""))
                if not user: time.sleep(.25); raise PanelError("用户名或密码错误", 401, "invalid_credentials")
                expires = int(time.time()) + SESSION_LIFETIME
                token = sign_session(self.panel.state["sessionSecret"], user["username"], expires)
                body = json.dumps({"username": user["username"], "role": user["role"], "expiresAt": expires}).encode()
                self.send_response(200); self.security_headers(); self.send_header("Content-Type", "application/json"); self.send_header("Set-Cookie", "%s=%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax" % (SESSION_COOKIE, token, SESSION_LIFETIME)); self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body); return
            if path == "/api/v1/auth/logout":
                self.send_response(204); self.send_header("Set-Cookie", "%s=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax" % SESSION_COOKIE); self.end_headers(); return
            if path == "/api/v1/proxies":
                if not self.require_admin(): return
                data = self.body_json(); owner = data.get("owner") or self.panel.cfg.admin_username; count = int(data.get("count") or 1); port = data.get("port")
                proxies = self.panel.create_proxies(owner, data.get("protocol", "hy2"), count, int(port) if port is not None else None)
                self.json_response(201, {"proxies": proxies, "count": len(proxies), "owner": owner}); return
            if path == "/api/v1/users":
                if not self.require_admin(): return
                data = self.body_json(); user = self.panel.add_user(data.get("username", ""), data.get("password", "")); view = next(v for v in self.panel.users_view() if v["username"] == user["username"]); view["subscriptionUrl"] = self.subscription_url(view.pop("subscriptionToken")); self.json_response(201, view); return
            if path.endswith("/rotate") and path.startswith("/api/v1/proxies/"):
                user = self.require_user()
                if user: self.json_response(200, self.panel.rotate_proxy(user["username"], path.split("/")[-2]))
                return
            if path == "/api/v1/proxies/rotate-all":
                user = self.require_user()
                if not user: return
                self.json_response(202, self.start_rotate_job(user["username"])); return
            raise PanelError("接口不存在", 404, "not_found")
        except Exception as error:
            self.error_response(error)

    def do_DELETE(self):
        path = urllib.parse.urlsplit(self.path).path
        try:
            if path.startswith("/api/v1/proxies/"):
                user = self.require_user()
                if user: self.panel.delete_proxy(user["username"], path.rsplit("/", 1)[-1]); self.send_response(204); self.end_headers()
                return
            if path.startswith("/api/v1/users/"):
                if not self.require_admin(): return
                self.panel.delete_user(urllib.parse.unquote(path.rsplit("/", 1)[-1])); self.send_response(204); self.end_headers(); return
            raise PanelError("接口不存在", 404, "not_found")
        except Exception as error:
            self.error_response(error)

    def do_PUT(self):
        path = urllib.parse.urlsplit(self.path).path
        try:
            if not self.require_admin(): return
            data = self.body_json()
            if path in ("/api/v1/network", "/api/v1/network/prefix"):
                info = self.panel.update_network(data.get("interface", ""), data.get("prefix", "")); self.json_response(200, {"network": info}); return
            if path == "/api/v1/subscription/settings":
                rules = normalize_direct_rules(data.get("directRules", [])); self.panel.state["directRules"] = rules; self.panel.save(); self.json_response(200, {"directRules": rules}); return
            raise PanelError("接口不存在", 404, "not_found")
        except Exception as error:
            self.error_response(error)

    def serve_static(self, path):
        public = path in ("/login.html", "/login.js") or path.endswith(".css")
        if not public and not self.identity():
            self.send_response(303); self.send_header("Location", "/login.html"); self.end_headers(); return
        if path == "/": path = "/index.html"
        file_path = (WEB_ROOT / path.lstrip("/")).resolve()
        if WEB_ROOT.resolve() not in file_path.parents or not file_path.is_file():
            file_path = WEB_ROOT / "index.html"
        content = file_path.read_bytes(); content_type = "text/html; charset=utf-8"
        if file_path.suffix == ".js": content_type = "application/javascript; charset=utf-8"
        elif file_path.suffix == ".css": content_type = "text/css; charset=utf-8"
        self.send_response(200); self.security_headers(); self.send_header("Content-Type", content_type); self.send_header("Content-Length", str(len(content))); self.end_headers(); self.wfile.write(content)


class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main():
    cfg = Config()
    panel = Panel(cfg)
    server = None
    try:
        panel.start()
        host, port = cfg.web_listen.rsplit(":", 1)
        server = Server((host, int(port)), Handler)
        server.panel = panel
        if cfg.tls_enabled:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            context.load_cert_chain(cfg.tls_cert_file, cfg.tls_key_file)
            server.socket = context.wrap_socket(server.socket, server_side=True)
        def shutdown(_signum, _frame):
            threading.Thread(target=server.shutdown, daemon=True).start()
        signal.signal(signal.SIGTERM, shutdown)
        signal.signal(signal.SIGINT, shutdown)
        print("Python 面板已启动：%s://%s，网卡=%s，前缀=%s，线路=%d" % ("https" if cfg.tls_enabled else "http", cfg.web_listen, panel.network["interface"], panel.network["prefix"], len(panel.state["proxies"])), flush=True)
        server.serve_forever()
    finally:
        panel.stop()
        if server:
            server.server_close()


if __name__ == "__main__":
    main()
