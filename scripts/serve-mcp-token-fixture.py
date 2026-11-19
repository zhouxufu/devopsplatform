"""Isolated token UI regression fixture; all token mutations are synthetic."""
import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import subprocess
import tempfile
import time
from urllib.error import HTTPError
from urllib.request import urlopen

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True)
parser.add_argument("--port", type=int, default=20081)
parser.add_argument("--host", default="127.0.0.1", help="Use a specific local LAN address to test HTTP clipboard restrictions")
args = parser.parse_args()
data = tempfile.TemporaryDirectory(prefix="mcp-token-ui-fixture-")
port = args.port - 1
child = subprocess.Popen([str(Path(args.binary).resolve()), "-admin-addr", "127.0.0.1:" + str(port), "-data", data.name, "-deploy", "http://127.0.0.1:1"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
FAKE_SECRET = "lp_dev_" + "0" * 64


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def json(self, value, status=200):
        raw = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        path = self.path.removeprefix("/api/plugins/devtools")
        if path == "/__fixture/embed":
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(b'<!doctype html><html><body style="margin:0"><iframe title="MCP HTTP copy test" src="/api/plugins/devtools/ui" style="width:100%;height:100vh;border:0"></iframe></body></html>')
            return
        if path == "/__fixture/paste":
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(b'<!doctype html><html><body><label for="paste">Paste verification</label><textarea id="paste" rows="14" style="width:95%"></textarea></body></html>')
            return
        fixed = {"/api/resources/environments": [{"name": "dev", "display_name": "开发环境"}, {"name": "test", "display_name": "测试环境"}, {"name": "prod", "protected": True}], "/api/registry/services": [{"name": "orders"}], "/api/me": {"username": "fixture", "role": "admin"}, "/api/tokens": self.server.tokens, "/__fixture/stats": {"creates": self.server.creates, "token_count": len(self.server.tokens)}}
        if path in fixed:
            return self.json(fixed[path])
        try:
            response = urlopen("http://127.0.0.1:" + str(port) + path)
        except HTTPError as error:
            response = error
        raw = response.read()
        if path.split("?")[0].endswith("/ui") and "dark=1" in self.path:
            raw = raw.replace(b"<head>", b'<head><script>localStorage.setItem("lp-theme","dark")</script>', 1)
        if path.split("?")[0].endswith("/ui") and "clipboard=denied" in self.path:
            raw = raw.replace(b"<head>", b'<head><script>Object.defineProperty(navigator,"clipboard",{get:()=>({writeText:()=>Promise.reject(new DOMException("Fixture denial","NotAllowedError"))})})</script>', 1)
        if path.split("?")[0].endswith("/ui") and "selection=blocked" in self.path:
            raw = raw.replace(b"<head>", b'<head><script>document.execCommand=()=>false</script>', 1)
        self.send_response(response.status)
        self.send_header("Content-Type", response.headers.get("Content-Type", "application/octet-stream"))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        path = self.path.removeprefix("/api/plugins/devtools")
        if path != "/api/tokens":
            return self.json({"error": "Fixture rejects all other writes"}, 403)
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        self.server.creates += 1
        time.sleep(0.2)  # Exercise duplicate submission protection.
        token = {"id": str(self.server.creates), "name": body["name"], "prefix": FAKE_SECRET[:15], "scopes": body["scopes"], "applications": body["applications"], "environments": body["environments"], "expires_at": int(time.time()) + 86400, "last_used_at": 0}
        self.server.tokens.append(token)
        self.json({"token": token, "secret": FAKE_SECRET}, 201)


server = ThreadingHTTPServer((args.host, args.port), Handler)
server.creates, server.tokens = 0, []
try:
    server.serve_forever()
finally:
    server.server_close()
    child.terminate()
    child.wait(timeout=10)
    data.cleanup()
