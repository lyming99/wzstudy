import http.cookiejar
import json
import os
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer

AGENT_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REPO_ROOT = os.path.dirname(AGENT_ROOT)
ROOT = REPO_ROOT
SERVER = "http://127.0.0.1:18091"
PAGE_PORT = 18092
MODEL_PORT = 18093
ADMIN_KEY = "a" * 40
READ_KEY = "r" * 40
ADMIN_EMAIL = "agent-e2e@example.com"
ADMIN_PASSWORD = "AgentE2E-Passw0rd!"

page_html = """<!doctype html><html><head><title>E2E 示例文章</title></head>
<body><article><h1>E2E 示例文章</h1><p>这是端到端联调使用的正文。</p></article></body></html>"""


class PageHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = page_html.encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


class ModelHandler(BaseHTTPRequestHandler):
    counter = 0

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        _ = self.rfile.read(length)
        ModelHandler.counter += 1
        step = ModelHandler.counter
        if step == 1:
            arguments = json.dumps({"url": f"http://127.0.0.1:{PAGE_PORT}/article"})
            name = "fetch_url"
        elif step == 2:
            arguments = json.dumps({"items": [{
                "external_id": "e2e-1",
                "title": "E2E 示例文章",
                "summary": "端到端联调摘要",
                "content": "这是经过整理的正文要点。",
                "format": "markdown",
                "url": f"http://127.0.0.1:{PAGE_PORT}/article",
            }]})
            name = "ingest_items"
        else:
            arguments = json.dumps({"summary": "端到端采集完成"})
            name = "finish_task"
        payload = {
            "choices": [{
                "message": {
                    "content": "",
                    "tool_calls": [{
                        "id": f"call_{step}",
                        "type": "function",
                        "function": {"name": name, "arguments": arguments},
                    }],
                },
                "finish_reason": "tool_calls",
            }],
            "usage": {"prompt_tokens": 10, "completion_tokens": 5},
        }
        body = json.dumps(payload).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def wait_for(url, timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if response.status < 500:
                    return
        except Exception:
            time.sleep(0.3)
    raise RuntimeError(f"service did not become ready: {url}")


def main():
    server_exe = os.path.join(ROOT, ".tmp", "wzstudy-e2e.exe")
    agent_exe = os.path.join(ROOT, ".tmp", "wzstudy-agent-e2e.exe")
    subprocess.run(["go", "build", "-o", server_exe, "./cmd/api"], cwd=ROOT, check=True)
    subprocess.run(["go", "build", "-o", agent_exe, "./cmd/wzstudy-agent"], cwd=AGENT_ROOT, check=True)

    page = HTTPServer(("127.0.0.1", PAGE_PORT), PageHandler)
    model = HTTPServer(("127.0.0.1", MODEL_PORT), ModelHandler)
    threading.Thread(target=page.serve_forever, daemon=True).start()
    threading.Thread(target=model.serve_forever, daemon=True).start()

    env = dict(os.environ)
    env.update({
        "APP_ENV": "development",
        "HTTP_ADDR": "127.0.0.1:18091",
        "STORE": "memory",
        "DATA_DIR": os.path.join(ROOT, ".tmp", "e2e-data"),
        "ADMIN_API_KEY": ADMIN_KEY,
        "READ_API_KEY": READ_KEY,
        "SYSTEM_ADMIN_EMAIL": ADMIN_EMAIL,
        "SYSTEM_ADMIN_PASSWORD": ADMIN_PASSWORD,
        "POLL_ENABLED": "false",
        "AGENT_ENABLED": "true",
        "AGENT_LEASE_SECONDS": "120",
    })
    server_log = open(os.path.join(ROOT, ".tmp", "e2e-server.log"), "wb")
    server = subprocess.Popen([server_exe], env=env, stdout=server_log, stderr=subprocess.STDOUT)
    agent = None
    try:
        wait_for(SERVER + "/api/v1/health/live")
        jar = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

        def call(method, path, body=None, bearer=None):
            data = json.dumps(body).encode("utf-8") if body is not None else None
            request = urllib.request.Request(SERVER + path, data=data, method=method)
            if data is not None:
                request.add_header("Content-Type", "application/json")
                request.add_header("Origin", SERVER)
            if bearer:
                request.add_header("Authorization", "Bearer " + bearer)
            try:
                with opener.open(request, timeout=20) as response:
                    raw = response.read()
                    return response.status, json.loads(raw) if raw else None
            except urllib.error.HTTPError as error:
                raw = error.read()
                raise RuntimeError(f"{method} {path} -> {error.code}: {raw[:400]!r}")

        call("POST", "/api/v1/auth/login", {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD})
        status, created = call("POST", "/api/v1/workspace/sources", {"slug": "e2e-source", "name": "E2E Source", "integration": "agent"})
        source_id = created["data"]["source"]["id"]
        if created["data"]["source"]["integration"] != "agent":
            raise RuntimeError("agent integration was not stored on the source")
        status, identity = call("POST", "/api/v1/admin/agent/agents", {"slug": "e2e-agent", "name": "E2E Agent", "description": "e2e"})
        agent_key = identity["data"]["access_key"]
        status, admin_status = call("GET", "/api/v1/admin/agent/status")
        if admin_status["data"]["enabled"] is not True:
            raise RuntimeError(f"task board sees a disabled module: {admin_status}")
        status, agent_list = call("GET", "/api/v1/admin/agent/agents")
        if not any(item["id"] == identity["data"]["agent"]["id"] for item in agent_list["data"]):
            raise RuntimeError("task board agent list is missing the created agent")
        status, source_list = call("GET", "/api/v1/workspace/sources")
        if not any(item["id"] == source_id for item in source_list["data"]):
            raise RuntimeError("task board source list is missing the created source")
        status, task = call("POST", "/api/v1/admin/agent/tasks", {
            "source_id": source_id,
            "goal": "采集本地示例文章并入库一条",
            "constraints": {"max_items": 3},
            "budget": {"max_rounds": 4, "max_tool_calls": 6, "timeout_seconds": 120},
        })
        task_id = task["data"]["id"]
        print("task created:", task_id)

        dist_exe = os.path.join(REPO_ROOT, "dist", "wzstudy-agent.exe")
        if os.path.exists(dist_exe):
            doctor_dir = os.path.join(ROOT, ".tmp", "e2e-doctor")
            os.makedirs(doctor_dir, exist_ok=True)
            doctor_config = os.path.join(doctor_dir, "agent.env")
            with open(doctor_config, "w", encoding="utf-8") as handle:
                handle.write(f"WZSTUDY_SERVER_URL={SERVER}\n")
                handle.write(f"WZSTUDY_AGENT_KEY={agent_key}\n")
                handle.write(f"AGENT_MODEL_BASE_URL=http://127.0.0.1:{MODEL_PORT}\n")
                handle.write("AGENT_MODEL=e2e-model\n")
            doctor_env = dict(os.environ)
            doctor_env["AGENT_CONFIG"] = doctor_config
            subprocess.run([dist_exe, "doctor"], env=doctor_env, cwd=doctor_dir, check=True)
            print("packaged agent doctor OK")

        agent_env = dict(os.environ)
        agent_env.update({
            "WZSTUDY_SERVER_URL": SERVER,
            "WZSTUDY_AGENT_KEY": agent_key,
            "AGENT_MODEL_BASE_URL": f"http://127.0.0.1:{MODEL_PORT}",
            "AGENT_MODEL": "e2e-model",
            "AGENT_SKILLS_DIR": os.path.join(AGENT_ROOT, "skills"),
            "AGENT_WORK_DIR": os.path.join(ROOT, ".tmp", "e2e-agent-state"),
            "AGENT_ALLOW_PRIVATE_NETWORK": "true",
            "AGENT_LOG_LEVEL": "debug",
        })
        agent_log = open(os.path.join(ROOT, ".tmp", "e2e-agent.log"), "wb")
        agent = subprocess.Popen([agent_exe, "once"], env=agent_env, stdout=agent_log, stderr=subprocess.STDOUT)
        if agent.wait(timeout=120) != 0:
            raise RuntimeError("agent exited with a non-zero status; see .tmp/e2e-agent.log")

        status, detail = call("GET", f"/api/v1/admin/agent/tasks/{task_id}")
        stored = detail["data"]
        print("task status:", stored["status"], "stats:", stored["stats"])
        status, listing = call("GET", "/api/v1/admin/agent/tasks?limit=50")
        if not any(item["id"] == task_id for item in listing["data"]["tasks"]):
            raise RuntimeError("task board list is missing the dispatched task")
        if stored["status"] != "succeeded":
            raise RuntimeError(f"task did not succeed: {stored}")
        if stored["stats"]["items_created"] != 1:
            raise RuntimeError(f"unexpected ingest stats: {stored['stats']}")

        status, events = call("GET", f"/api/v1/admin/agent/tasks/{task_id}/events")
        kinds = [event["kind"] for event in events["data"]]
        print("events:", kinds)
        for required in ("round_start", "tool_call", "tool_result", "finish"):
            if required not in kinds:
                raise RuntimeError(f"missing event {required}: {kinds}")

        status, intakes = call("GET", f"/api/v1/workspace/intakes?limit=10")
        agent_intakes = [entry for entry in intakes["data"]["items"] if entry.get("task_id") == task_id]
        print("intake receipts for task:", len(agent_intakes), agent_intakes[0]["integration"] if agent_intakes else None)
        if not agent_intakes or agent_intakes[0]["integration"] != "agent":
            raise RuntimeError("task intake receipt is missing or not attributed to the agent")

        status, items = call("GET", "/api/v1/workspace/items?limit=5")
        titles = [item["draft"]["title"] for item in items["data"]["items"]]
        print("published items:", titles)
        if "E2E 示例文章" not in titles:
            raise RuntimeError("ingested item was not published")

        boundary = "----wzstudy" + uuid.uuid4().hex
        chunks = []
        for key, value in {"version": "9.9.9", "platform": "windows", "arch": "amd64", "notes": "e2e release"}.items():
            chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
        chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="e2e-agent.exe"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode())
        chunks.append(b"E2E-AGENT-BINARY")
        chunks.append(f"\r\n--{boundary}--\r\n".encode())
        upload = urllib.request.Request(SERVER + "/api/v1/admin/agent/releases", data=b"".join(chunks), method="POST")
        upload.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
        upload.add_header("Origin", SERVER)
        with opener.open(upload, timeout=20) as response:
            release = json.loads(response.read())["data"]
        if release["version"] != "9.9.9" or release["size"] != len(b"E2E-AGENT-BINARY"):
            raise RuntimeError(f"unexpected release: {release}")
        status, release_list = call("GET", "/api/v1/admin/agent/releases")
        if not any(item["id"] == release["id"] for item in release_list["data"]):
            raise RuntimeError("release list is missing the uploaded package")
        with opener.open(SERVER + f"/api/v1/admin/agent/releases/{release['id']}/download", timeout=20) as response:
            if response.read() != b"E2E-AGENT-BINARY":
                raise RuntimeError("downloaded package differs from the upload")
        call("POST", f"/api/v1/admin/agent/releases/{release['id']}/delete", {})
        status, release_list = call("GET", "/api/v1/admin/agent/releases")
        if any(item["id"] == release["id"] for item in release_list["data"]):
            raise RuntimeError("release was not deleted")
        print("release management OK")

        print("E2E OK")
        return 0
    finally:
        if agent and agent.poll() is None:
            agent.terminate()
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
        server_log.close()
        page.shutdown()
        model.shutdown()


if __name__ == "__main__":
    sys.exit(main())
