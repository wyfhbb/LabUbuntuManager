#!/usr/bin/env python3
"""假的企业微信机器人 Webhook，供 notify test / notify check 打通推送链路。

收到 POST 就把消息体追加写进 /tmp/webhook-received.log，并按企业微信的成功响应
返回 {"errcode":0,"errmsg":"ok"}，让 server-mgr 走完"推送成功 → 记入去重状态"这条路径。
"""
import http.server
import json

RECEIVED = "/tmp/webhook-received.log"


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        with open(RECEIVED, "a", encoding="utf-8") as f:
            try:
                content = json.loads(body)["text"]["content"]
            except Exception:
                content = body.decode("utf-8", "replace")
            f.write("=== 收到一条推送 ===\n" + content + "\n")
        payload = b'{"errcode":0,"errmsg":"ok"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    http.server.HTTPServer(("127.0.0.1", 18080), Handler).serve_forever()
