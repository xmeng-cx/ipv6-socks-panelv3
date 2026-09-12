import json
import http.client
import ipaddress
import tempfile
import threading
import unittest
from pathlib import Path
from types import SimpleNamespace

import app


class ConfigStub:
    def __init__(self, data_dir):
        self.data_dir = Path(data_dir)
        self.admin_username = "xmeng"
        self.admin_password = "5201314"
        self.initial_proxies = 0
        self.base_port = 20000
        self.max_proxies = 100
        self.dad_timeout = 1


class RunningProcess:
    @staticmethod
    def poll():
        return None


class HTTPPanelStub:
    def __init__(self):
        self.cfg = ConfigStub(tempfile.gettempdir())
        self.cfg.initial_proxies = 10
        self.cfg.advertise_host = ""
        self.cfg.socks_udp = True
        self.cfg.hy2_obfs_password = "5201314"
        self.state = {"sessionSecret": "test-secret", "directRules": [], "proxies": [], "users": [
            app.new_user("xmeng", "5201314", "admin"), app.new_user("alice", "alice123", "user")
        ]}
        self.network = {"interface": "eth0", "prefix": "2001:db8::/64", "ipv4": "192.0.2.1"}
        self.xray = RunningProcess()
        self.created = None
        self.jobs = {}
        self.rotated = []

    def authenticate(self, username, password):
        return next((u for u in self.state["users"] if u["username"] == username and ((username == "xmeng" and password == "5201314") or (username == "alice" and password == "alice123"))), None)

    def find_user(self, username, state):
        return next((u for u in state["users"] if u["username"] == username), None)

    def list_for_user(self, username):
        return [{"id": "20000", "port": 20000, "ipv6": "2001:db8::1", "owner": username, "protocol": "hy2"}]

    def rotate_proxy(self, username, proxy_id):
        self.rotated.append((username, proxy_id))
        return {"id": proxy_id, "ipv6": "2001:db8::2"}

    def create_proxies(self, owner, protocol, count, port):
        self.created = (owner, protocol, count, port)
        return [{"id": "20000", "port": 20000, "owner": owner, "protocol": protocol}]


class PanelPythonTests(unittest.TestCase):
    def test_state_migration_and_authentication(self):
        with tempfile.TemporaryDirectory() as directory:
            user = app.new_user("xmeng", "5201314", "admin")
            user.pop("subscriptionToken")
            state = {"version": 2, "users": [user], "proxies": [], "pending": {}}
            Path(directory, "state.json").write_text(json.dumps(state), "utf-8")
            panel = app.Panel(ConfigStub(directory))
            self.assertEqual(panel.state["version"], app.STATE_VERSION)
            self.assertTrue(panel.state["users"][0]["subscriptionToken"])
            self.assertEqual(panel.state["directRules"], app.DEFAULT_DIRECT_RULES)
            self.assertEqual(panel.authenticate("xmeng", "5201314")["role"], "admin")
            self.assertIsNone(panel.authenticate("xmeng", "wrong-password"))

    def test_session_signing(self):
        token = app.sign_session("secret", "alice", 4102444800)
        self.assertEqual(app.parse_session("secret", token), "alice")
        self.assertIsNone(app.parse_session("other", token))

    def test_direct_rule_normalization(self):
        values = app.normalize_direct_rules(["Example.COM", "1.1.1.1", "2001:db8::/32", "example.com"])
        self.assertEqual(values, ["example.com", "1.1.1.1", "2001:db8::/32"])
        self.assertEqual(app.mihomo_direct_rule("1.1.1.1"), "IP-CIDR,1.1.1.1/32,DIRECT,no-resolve")
        self.assertEqual(app.mihomo_direct_rule("2001:db8::/32"), "IP-CIDR6,2001:db8::/32,DIRECT,no-resolve")
        self.assertEqual(app.mihomo_direct_rule("example.com"), "DOMAIN,example.com,DIRECT")
        self.assertEqual(app.mihomo_direct_rule("*.example.com"), "DOMAIN-SUFFIX,example.com,DIRECT")

    def test_mihomo_android_root_template_and_default_proxy_group(self):
        config = app.mihomo_config([
            {"protocol": "hy2", "port": 20000, "username": "alice", "password": "secret"}
        ], "192.0.2.10", "5201314", app.DEFAULT_DIRECT_RULES + ["example.cn"])
        self.assertIn("mode: rule", config)
        self.assertIn("external-ui: /data/adb/mihomo/ui", config)
        self.assertIn("device: mihomo", config)
        self.assertIn("strict-route: true", config)
        self.assertIn('name: \"US-HY20000\"', config)
        self.assertIn("type: hysteria2", config)
        self.assertIn("DOMAIN,example.cn,DIRECT", config)
        self.assertIn("IP-CIDR,101.35.154.10/32,DIRECT,no-resolve", config)
        self.assertIn("IP-CIDR,192.0.2.10/32,DIRECT,no-resolve", config)
        self.assertIn("IP-CIDR6,fe80::/10,DIRECT,no-resolve", config)
        self.assertIn("DOMAIN,www.meiguodizhi.com,DIRECT", config)
        self.assertTrue(config.rstrip().endswith("MATCH,全局代理"))

    def test_only_admin_can_create_lines_for_an_account(self):
        panel = HTTPPanelStub()
        server = app.Server(("127.0.0.1", 0), app.Handler)
        server.panel = panel
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            def login(username, password):
                connection = http.client.HTTPConnection("127.0.0.1", server.server_port)
                connection.request("POST", "/api/v1/auth/login", json.dumps({"username": username, "password": password}), {"Content-Type": "application/json"})
                response = connection.getresponse()
                response.read()
                self.assertEqual(response.status, 200)
                return response.getheader("Set-Cookie").split(";", 1)[0]

            alice_cookie = login("alice", "alice123")
            connection = http.client.HTTPConnection("127.0.0.1", server.server_port)
            connection.request("POST", "/api/v1/proxies", "{}", {"Content-Type": "application/json", "Cookie": alice_cookie})
            response = connection.getresponse()
            response.read()
            self.assertEqual(response.status, 403)

            admin_cookie = login("xmeng", "5201314")
            connection = http.client.HTTPConnection("127.0.0.1", server.server_port)
            connection.request("POST", "/api/v1/proxies", json.dumps({"owner": "alice", "protocol": "hy2", "count": 10}), {"Content-Type": "application/json", "Cookie": admin_cookie})
            response = connection.getresponse()
            response.read()
            self.assertEqual(response.status, 201)
            self.assertEqual(panel.created, ("alice", "hy2", 10, None))

            connection = http.client.HTTPConnection("127.0.0.1", server.server_port)
            connection.request("GET", "/api/v1/health", headers={"Cookie": admin_cookie, "Host": "panel.example.com", "X-Forwarded-Proto": "https"})
            response = connection.getresponse()
            health = json.loads(response.read())
            self.assertEqual(response.status, 200)
            self.assertTrue(health["subscriptionUrl"].startswith("https://panel.example.com/sub/"))
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_get_rotates_all_ips_by_username_without_login(self):
        panel = HTTPPanelStub()
        server = app.Server(("127.0.0.1", 0), app.Handler)
        server.panel = panel
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            connection = http.client.HTTPConnection("127.0.0.1", server.server_port)
            connection.request("GET", "/api/v1/rotate-ip?username=alice")
            response = connection.getresponse()
            payload = json.loads(response.read())
            self.assertEqual(response.status, 202)
            self.assertEqual(payload["total"], 1)
            for _ in range(20):
                if panel.rotated:
                    break
                threading.Event().wait(.01)
            self.assertEqual(panel.rotated, [("alice", "20000")])

            panel.rotated.clear()
            connection = http.client.HTTPConnection("127.0.0.1", server.server_port)
            connection.request("GET", "/api/v1/rotate-ip?username=alice&port=20000")
            response = connection.getresponse()
            response.read()
            self.assertEqual(response.status, 202)
            for _ in range(100):
                if panel.rotated:
                    break
                threading.Event().wait(.01)
            self.assertEqual(panel.rotated, [("alice", "20000")])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_failed_first_line_removes_activated_address(self):
        panel = app.Panel.__new__(app.Panel)
        panel.lock = threading.RLock()
        panel.cfg = ConfigStub(tempfile.gettempdir())
        panel.state = {"users": [{"username": "alice", "proxyPassword": "secret"}], "proxies": []}
        panel.prefix = ipaddress.ip_network("2001:db8::/64")
        panel.network = {"interface": "eth0"}
        panel.xray = None
        activated, deleted, restarted = [], [], []
        panel.random_ip = lambda: ipaddress.ip_address("2001:db8::2")
        panel.add_address = activated.append
        panel.delete_address = deleted.append
        panel.check_egress = lambda _ip: (_ for _ in ()).throw(RuntimeError("egress failed"))
        panel.restart_xray = lambda: restarted.append(True)
        with self.assertRaisesRegex(RuntimeError, "egress failed"):
            panel._create_proxies("alice", "hy2", 1)
        self.assertEqual(activated, ["2001:db8::2"])
        self.assertEqual(deleted, ["2001:db8::2"])
        self.assertEqual(restarted, [])

    def test_existing_ipv6_is_accepted_independent_of_iproute_error_text(self):
        panel = app.Panel.__new__(app.Panel)
        panel.cfg = ConfigStub(tempfile.gettempdir())
        panel.prefix = ipaddress.ip_network("2001:db8::/64")
        panel.network = {"interface": "eth0"}
        responses = iter([
            SimpleNamespace(returncode=2, stdout="", stderr="Error: ipv6: address already assigned."),
            SimpleNamespace(returncode=0, stdout=json.dumps([{"addr_info": [{"local": "2001:db8::2", "family": "inet6"}]}]), stderr=""),
        ])
        panel.run = lambda *_args, **_kwargs: next(responses)
        panel.add_address(ipaddress.ip_address("2001:db8::2"))
if __name__ == "__main__":
    unittest.main()
