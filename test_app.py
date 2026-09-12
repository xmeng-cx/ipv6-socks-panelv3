import json
import http.client
import tempfile
import threading
import unittest
from pathlib import Path

import app


class ConfigStub:
    def __init__(self, data_dir):
        self.data_dir = Path(data_dir)
        self.admin_username = "xmeng"
        self.admin_password = "5201314"
        self.initial_proxies = 0
        self.base_port = 20000
        self.max_proxies = 100


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

    def authenticate(self, username, password):
        return next((u for u in self.state["users"] if u["username"] == username and ((username == "xmeng" and password == "5201314") or (username == "alice" and password == "alice123"))), None)

    def find_user(self, username, state):
        return next((u for u in state["users"] if u["username"] == username), None)

    def list_for_user(self, _username):
        return []

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

    def test_mihomo_defaults_to_global(self):
        config = app.mihomo_config([
            {"protocol": "hy2", "port": 20000, "username": "alice", "password": "secret"}
        ], "panel.example.com", "5201314", ["example.cn"])
        self.assertIn("mode: global", config)
        self.assertIn("type: hysteria2", config)
        self.assertIn("DOMAIN-SUFFIX,example.cn,DIRECT", config)

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
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)
if __name__ == "__main__":
    unittest.main()
