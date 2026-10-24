"""Real yt-dlp/FFmpeg integration against synthetic media on loopback only.

No Bilibili requests or WzStudy writes. Missing dependencies are explicit skips.
"""
from copy import deepcopy
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch

SKILL = Path(__file__).resolve().parents[1]
ROOT = SKILL.parents[1]
spec = importlib.util.spec_from_file_location("bilibili_local_integration_subject", SKILL / "scripts/bilibili_collect.py")
collector = importlib.util.module_from_spec(spec)
spec.loader.exec_module(collector)
BV = "BV0000000000"  # Synthetic, never requested from Bilibili.


class LocalHandler(SimpleHTTPRequestHandler):
    requests = []

    def do_GET(self):
        self.requests.append(self.path)
        super().do_GET()

    def log_message(self, *_args):
        pass


@unittest.skipUnless(importlib.util.find_spec("yt_dlp") and shutil.which("ffmpeg") and shutil.which("ffprobe"),
                     "Real local integration requires yt-dlp, FFmpeg and ffprobe")
class LocalIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        (ROOT / ".artifacts").mkdir(exist_ok=True)
        cls.temp = tempfile.TemporaryDirectory(prefix="bilibili-local-", dir=ROOT / ".artifacts")
        cls.addClassCleanup(cls.temp.cleanup)
        cls.base = Path(cls.temp.name)
        cls.fixtures = cls.base / "fixtures"
        cls.fixtures.mkdir()
        commands = [
            ["ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=10",
             "-t", "0.5", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", str(cls.fixtures / "source-video.mp4")],
            ["ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000",
             "-t", "0.5", "-vn", "-c:a", "aac", str(cls.fixtures / "source-audio.m4a")],
        ]
        for command in commands:
            subprocess.run(command, check=True, capture_output=True, timeout=30)
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), partial(LocalHandler, directory=str(cls.fixtures)))
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.addClassCleanup(cls.server.server_close)
        cls.addClassCleanup(cls.server.shutdown)
        cls.url = f"http://127.0.0.1:{cls.server.server_address[1]}"
        cls.info = {"id": BV, "title": "本地合成音视频验证", "uploader": "合成测试作者", "duration": 0.5,
                    "timestamp": 1700000000,
                    "subtitles": {"ai-zh": [{"ext": "srt", "data": "1\n00:00:00,000 --> 00:00:00,500\n合成测试\n"}]},
                    "formats": [
                        {"format_id": "v", "url": cls.url + "/source-video.mp4", "ext": "mp4", "height": 90,
                         "width": 160, "vcodec": "avc1.42001e", "acodec": "none", "fps": 10,
                         "filesize": (cls.fixtures / "source-video.mp4").stat().st_size},
                        {"format_id": "a", "url": cls.url + "/source-audio.m4a", "ext": "m4a",
                         "vcodec": "none", "acodec": "mp4a.40.2", "abr": 64,
                         "filesize": (cls.fixtures / "source-audio.m4a").stat().st_size},
                    ]}

    def setUp(self):
        LocalHandler.requests = []

    def args(self, name, *options):
        return collector.parser().parse_args(["collect", BV, "--output", str(self.base / name), *options])

    def test_real_default_metadata_never_downloads_media(self):
        from yt_dlp.extractor.bilibili import BiliBiliIE
        with patch.object(BiliBiliIE, "_real_extract", return_value=deepcopy(self.info)) as extractor:
            result = collector.collect(self.args("metadata"))
        self.assertTrue(result["ok"], result)
        self.assertEqual(extractor.call_count, 1)
        self.assertEqual(result["link_items"], 1)
        self.assertEqual(result["downloaded"], 0)
        self.assertEqual(LocalHandler.requests, [])

    def test_real_download_merge_subtitle_and_push_preflight(self):
        from yt_dlp import YoutubeDL
        from yt_dlp.extractor.bilibili import BiliBiliIE
        diagnostics = []
        original = YoutubeDL.process_ie_result

        def process(ydl, *args, **kwargs):
            try:
                return original(ydl, *args, **kwargs)
            except Exception as error:
                # Diagnostics contain only this test's synthetic loopback fixture.
                diagnostics.append(type(error).__name__ + ": " + str(error))
                raise

        with patch.object(BiliBiliIE, "_real_extract", return_value=deepcopy(self.info)) as extractor, \
             patch.object(YoutubeDL, "process_ie_result", process):
            result = collector.collect(self.args("video 中文 % output", "--download", "--subtitles", "--max-size-mib", "1"))
        self.assertTrue(result["ok"], (result, diagnostics))
        self.assertEqual(extractor.call_count, 1)
        self.assertEqual(result["downloaded"], 1)
        folder = self.base / "video 中文 % output"
        video = folder / result["records"][0]["video"]
        self.assertTrue(video.is_file())
        probe = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "json", str(video)],
                               check=True, capture_output=True, text=True, timeout=30)
        streams = {stream["codec_type"] for stream in json.loads(probe.stdout)["streams"]}
        self.assertEqual(streams, {"audio", "video"})
        self.assertTrue((folder / "001/subtitles.ai-zh.srt").is_file())
        self.assertIn("/source-video.mp4", LocalHandler.requests)
        self.assertIn("/source-audio.m4a", LocalHandler.requests)
        script = ROOT / "skills/wzstudy-push/scripts/wzstudy_push.py"
        import sys
        for command in ([sys.executable, str(script), "push", "--input", str(folder / "items.json"), "--dry-run"],
                        [sys.executable, str(script), "upload", "--metadata", str(folder / "001/metadata.json"),
                         "--file", str(video), "--dry-run"]):
            preflight = subprocess.run(command, capture_output=True, text=True, encoding="utf-8", timeout=30)
            self.assertEqual(preflight.returncode, 0, preflight.stdout + preflight.stderr)
            self.assertTrue(json.loads(preflight.stdout)["dry_run"])
        raw = (folder / "manifest.json").read_text(encoding="utf-8")
        self.assertNotIn(self.url, raw)  # Internal media URLs never become source metadata.

    def test_real_backend_playlist_refused_without_expansion(self):
        from yt_dlp.extractor.bilibili import BiliBiliIE
        playlist = {"_type": "playlist", "id": BV, "title": "不应展开", "entries": [deepcopy(self.info)]}
        with patch.object(BiliBiliIE, "_real_extract", return_value=playlist) as extractor:
            result = collector.collect(self.args("playlist"))
        self.assertFalse(result["ok"])
        self.assertEqual(result["records"][0]["error"], "unsupported_result")
        self.assertEqual(extractor.call_count, 1)
        self.assertEqual(result["link_items"], 0)
        self.assertEqual(LocalHandler.requests, [])


if __name__ == "__main__":
    unittest.main()
