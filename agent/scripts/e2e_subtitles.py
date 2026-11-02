"""Isolated server + real agent + deterministic model: optional subtitle upload.

Uses only temporary memory-store data, loopback fixture endpoints and test keys.
It neither contacts a real model nor reads/deploys a running installation.
"""

import http.cookiejar
import io
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

AGENT_ROOT = Path(__file__).resolve().parents[1]
REPO_ROOT = AGENT_ROOT.parent
SRT = "1\n00:00:00,000 --> 00:00:02,000\n真实中文字幕\n" + "完整字幕内容\n" * 3000
VTT = "WEBVTT\n\n00:00.000 --> 00:02.000\nEnglish subtitle\n"


class SourceHandler(BaseHTTPRequestHandler):
    fetched = []

    def do_GET(self):
        SourceHandler.fetched.append(self.path)
        body = SRT.encode("utf-8") if self.path == "/zh.srt" else b"not found"
        self.send_response(200 if self.path == "/zh.srt" else 404)
        self.send_header("Content-Type", "application/x-subrip; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


class ModelHandler(BaseHTTPRequestHandler):
    source_url = ""
    asset = None
    calls = []

    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        names = {entry["function"]["name"] for entry in payload.get("tools", [])}
        assert {"fetch_subtitle", "ingest_items", "finish_task"} <= names, names
        goal = next(entry["content"] for entry in payload["messages"] if entry["role"] == "user")
        mode = next(mode for mode in ("with", "skip", "prepared_skip", "missing") if f"[mode={mode}]" in goal)
        results = {entry["tool_call_id"]: entry["content"] for entry in payload["messages"] if entry["role"] == "tool"}
        if mode != "skip" and "subtitle-fetch" not in results:
            name, call_id = "fetch_subtitle", "subtitle-fetch"
            arguments = {"url": self.source_url + ("/missing.srt" if mode == "missing" else "/zh.srt")}
        elif "media-ingest" not in results:
            name, call_id = "ingest_items", "media-ingest"
            item = {
                "external_id": "subtitle-e2e-" + mode,
                "kind": "audio",
                "title": "字幕闭环测试 " + mode,
                "url": self.source_url + "/lesson",
                "assets": [self.asset],
            }
            if mode == "with":
                fetched = json.loads(results["subtitle-fetch"])
                assert fetched["available"] and fetched["uploaded"] is False
                item["subtitles"] = [
                    {"id": "zh", "language": "zh-CN", "ref": fetched["subtitle_ref"]},
                    {"id": "en", "language": "en", "format": "vtt", "content": VTT},
                ]
            elif mode == "missing":
                assert json.loads(results["subtitle-fetch"])["available"] is False
            # skip/prepared_skip deliberately omit subtitles: fetching is not uploading.
            arguments = {"items": [item]}
        else:
            name, call_id = "finish_task", "media-finish"
            receipt = json.loads(results["media-ingest"])
            assert receipt["created"] == 1, receipt
            arguments = {"summary": "媒体入库完成；字幕由 AI 按需选择"}
        ModelHandler.calls.append((mode, name))
        body = json.dumps({"choices": [{"message": {"role": "assistant", "content": "", "tool_calls": [
            {"id": call_id, "type": "function", "function": {"name": name, "arguments": json.dumps(arguments)}}
        ]}, "finish_reason": "tool_calls"}]}).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def wait_for(url, process):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("isolated API process exited during startup")
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if response.status == 200:
                    return
        except (OSError, urllib.error.URLError):
            time.sleep(0.2)
    raise RuntimeError("isolated API startup timed out")


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def main():
    artifact_root = REPO_ROOT / ".tmp"
    artifact_root.mkdir(exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="subtitle-e2e-", dir=artifact_root))
    suffix = ".exe" if os.name == "nt" else ""
    server_exe, agent_exe = work / ("api" + suffix), work / ("agent" + suffix)
    subprocess.run(["go", "build", "-o", str(server_exe), "./cmd/api"], cwd=REPO_ROOT, check=True)
    subprocess.run(["go", "build", "-o", str(agent_exe), "./cmd/wzstudy-agent"], cwd=AGENT_ROOT, check=True)
    source = ThreadingHTTPServer(("127.0.0.1", 0), SourceHandler)
    model = ThreadingHTTPServer(("127.0.0.1", 0), ModelHandler)
    ModelHandler.source_url = f"http://127.0.0.1:{source.server_port}"
    for endpoint in (source, model):
        threading.Thread(target=endpoint.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{free_port()}"
    admin_email, admin_password = "subtitle-e2e@example.com", "Subtitle-E2E-test-123!"
    env = dict(os.environ)
    env.update({
        "APP_ENV": "development", "HTTP_ADDR": base.removeprefix("http://"),
        "STORE": "memory", "DATA_DIR": str(work / "data"),
        "ADMIN_API_KEY": "a" * 40, "READ_API_KEY": "r" * 40,
        "SYSTEM_ADMIN_EMAIL": admin_email, "SYSTEM_ADMIN_PASSWORD": admin_password,
        "POLL_ENABLED": "false", "AGENT_ENABLED": "true", "AGENT_LEASE_SECONDS": "120",
    })
    server_log = open(work / "server.log", "wb")
    server = subprocess.Popen([str(server_exe)], cwd=work, env=env, stdout=server_log, stderr=subprocess.STDOUT)
    try:
        wait_for(base + "/api/v1/health/live", server)
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

        def call(method, path, value=None, bearer=None, raw=None, content_type=None):
            body = json.dumps(value).encode("utf-8") if value is not None else raw
            request = urllib.request.Request(base + "/api/v1" + path, data=body, method=method)
            request.add_header("Origin", base)
            if body is not None:
                request.add_header("Content-Type", content_type or "application/json")
            if bearer:
                request.add_header("Authorization", "Bearer " + bearer)
            try:
                with opener.open(request, timeout=30) as response:
                    data = response.read()
                    return json.loads(data)["data"] if data else None
            except urllib.error.HTTPError as error:
                raise RuntimeError(f"{method} {path}: HTTP {error.code}, {error.read()[:300]!r}") from error

        call("POST", "/auth/login", {"email": admin_email, "password": admin_password})
        source_data = call("POST", "/workspace/sources", {"slug": "subtitle-e2e", "name": "Subtitle E2E", "integration": "agent"})["source"]
        identity = call("POST", "/admin/agent/agents", {"slug": "subtitle-e2e", "name": "Subtitle E2E"})
        agent_key = identity["access_key"]
        audio = io.BytesIO()
        with wave.open(audio, "wb") as writer:
            writer.setnchannels(1)
            writer.setsampwidth(2)
            writer.setframerate(8000)
            writer.writeframes(b"\x00\x00" * 8000)
        boundary = "subtitle-e2e-" + uuid.uuid4().hex
        raw = (f'--{boundary}\r\nContent-Disposition: form-data; name="files"; filename="lesson.wav"\r\nContent-Type: audio/wav\r\n\r\n'.encode()
               + audio.getvalue() + f"\r\n--{boundary}--\r\n".encode())
        uploaded = call("POST", "/agent/uploads", bearer=agent_key, raw=raw, content_type="multipart/form-data; boundary=" + boundary)
        ModelHandler.asset = {field: uploaded[field] for field in ("id", "name", "mime", "size")}
        config_path = work / "test-agent.env"
        config_path.write_text("# Isolated test: all configuration is explicitly supplied through environment.\n", encoding="utf-8")
        agent_env = dict(os.environ)
        agent_env.update({
            "AGENT_CONFIG": str(config_path), "WZSTUDY_SERVER_URL": base, "WZSTUDY_AGENT_KEY": agent_key,
            "AGENT_MODEL_BASE_URL": f"http://127.0.0.1:{model.server_port}", "AGENT_MODEL_API_KEY": "test-only",
            "AGENT_MODEL": "subtitle-fixture-model", "AGENT_SKILLS_DIR": str(AGENT_ROOT / "skills"),
            "AGENT_WORK_DIR": str(work / "agent-work"), "AGENT_ALLOW_PRIVATE_NETWORK": "true", "AGENT_LOG_LEVEL": "info",
        })
        summary = []
        for mode in ("with", "skip", "prepared_skip", "missing"):
            task = call("POST", "/admin/agent/tasks", {
                "source_id": source_data["id"], "assigned_agent_id": identity["agent"]["id"],
                "goal": f"[mode={mode}] 采集测试音频，字幕可由 AI 自主决定是否上传",
                "constraints": {"max_items": 1, "content_type": "audio"},
                "budget": {"max_rounds": 6, "max_tool_calls": 8, "timeout_seconds": 120},
            })
            with open(work / (mode + ".log"), "wb") as log:
                subprocess.run([str(agent_exe), "once"], cwd=work, env=agent_env, stdout=log, stderr=subprocess.STDOUT, timeout=90, check=True)
            stored = call("GET", "/admin/agent/tasks/" + task["id"])
            assert stored["status"] == "succeeded" and stored["stats"]["items_created"] == 1, stored
            entries = call("GET", "/workspace/items?limit=20")["items"]
            entry = next(entry for entry in entries if entry["draft"].get("external_id") == "subtitle-e2e-" + mode)
            tracks = entry["draft"].get("subtitles", [])
            assert entry["source_id"] == source_data["id"]
            assert entry["draft"]["assets"][0]["id"] == ModelHandler.asset["id"]
            if mode == "with":
                assert len(tracks) == 2 and tracks[0]["content"] == SRT and tracks[1]["content"] == VTT, tracks
                assert tracks[0]["language"] == "zh-CN" and tracks[1]["format"] == "vtt"
                assert all("ref" not in track for track in tracks)
            else:
                assert not tracks, (mode, tracks)
            summary.append({"mode": mode, "task_status": stored["status"], "subtitles": len(tracks)})
        assert ("skip", "fetch_subtitle") not in ModelHandler.calls
        assert SourceHandler.fetched.count("/zh.srt") == 2 and SourceHandler.fetched.count("/missing.srt") == 1
        (work / "result.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps({"passed": True, "scenarios": summary, "artifacts": str(work)}, ensure_ascii=True))
    finally:
        server.terminate()
        try:
            server.wait(timeout=10)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait(timeout=10)
        server_log.close()
        for endpoint in (source, model):
            endpoint.shutdown()
            endpoint.server_close()


if __name__ == "__main__":
    main()
