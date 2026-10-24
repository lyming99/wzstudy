#!/usr/bin/env python3
"""Bounded Bilibili collection; no browser credential discovery or WzStudy writes.

Python 3.11+. yt-dlp is optional for --dry-run/tests, required for real extraction.
The default saves factual metadata and source links, not video files or transcripts.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import http.client
import importlib.metadata
import io
import json
import math
from pathlib import Path
import re
import shutil
import sys
from urllib.parse import parse_qs, urlencode, urljoin, urlsplit

VIDEO_ID = r"(?:BV[0-9A-Za-z]{10}|av[1-9][0-9]*)"
VIDEO_HOSTS = {"bilibili.com", "www.bilibili.com", "m.bilibili.com"}
MAX_COOKIE_BYTES = 1024 * 1024
MAX_SUBTITLE_BYTES = 8 * 1024 * 1024


class CollectError(Exception):
    def __init__(self, code, message):
        self.code, self.message = code, message
        super().__init__(message)


def fail(code, message):
    raise CollectError(code, message)


def bounded(value, low, high, label):
    if isinstance(value, bool) or not isinstance(value, int) or not low <= value <= high:
        fail("invalid_option", f"{label} 必须是 {low}–{high} 的整数。")
    return value


def text(value, limit, label):
    if not isinstance(value, str) or not value.strip() or len(value.encode("utf-8")) > limit:
        fail("invalid_metadata", f"{label} 必须非空且不超过 {limit} UTF-8 字节。")
    return value.strip()


def normalize_url(value, part=None):
    """Allow only single video IDs/pages or b23 short links. Never fetch here."""
    if not isinstance(value, str):
        fail("invalid_url", "请提供 BV/av 号或哔哩哔哩视频链接。")
    value = value.strip()
    if not value or len(value) > 4096 or any(ord(c) <= 32 or ord(c) == 127 for c in value):
        fail("invalid_url", "链接为空、过长或包含非法字符。")
    if re.fullmatch(VIDEO_ID, value, re.IGNORECASE):
        value = "https://www.bilibili.com/video/" + value
    try:
        parsed = urlsplit(value)
        if parsed.scheme not in ("http", "https") or parsed.username or parsed.password or parsed.port is not None:
            raise ValueError()
        host = parsed.hostname
        query = parse_qs(parsed.query, keep_blank_values=True, max_num_fields=64)
        if part is None and "p" in query:
            if len(query["p"]) != 1 or not re.fullmatch(r"[0-9]+", query["p"][0]):
                raise ValueError()
            part = int(query["p"][0])
        if part is not None:
            bounded(part, 1, 10000, "分 P")
        if host == "b23.tv" and re.fullmatch(r"/[0-9A-Za-z]{1,64}/?", parsed.path):
            return "https://b23.tv" + parsed.path.rstrip("/") + ("?" + urlencode({"p": part}) if part else "")
        match = re.fullmatch(r"/video/(" + VIDEO_ID + r")/?", parsed.path, re.IGNORECASE)
        if host not in VIDEO_HOSTS or not match:
            raise ValueError()
        identity = match[1]
        identity = "BV" + identity[2:] if identity[:2].lower() == "bv" else "av" + identity[2:]
        return f"https://www.bilibili.com/video/{identity}/?p={part or 1}"
    except (ValueError, TypeError):
        fail("invalid_url", "仅接受 BV/av 单视频或 b23.tv 短链；不支持空间、合集、番剧、直播、端口或带凭据链接。")


def resolve_short(url, timeout=30):
    """Follow at most five b23 redirects; never request an off-allowlist host."""
    original_part = parse_qs(urlsplit(url).query).get("p", [None])[0]
    for _ in range(5):
        parsed = urlsplit(url)
        if parsed.hostname != "b23.tv":
            return normalize_url(url, int(original_part) if original_part else None)
        connection = http.client.HTTPSConnection("b23.tv", timeout=timeout)
        try:
            connection.request("GET", parsed.path + ("?" + parsed.query if parsed.query else ""),
                               headers={"User-Agent": "Mozilla/5.0", "Accept": "text/html"})
            response = connection.getresponse()
            location = response.getheader("Location")
            if response.status not in (301, 302, 303, 307, 308) or not location:
                fail("short_link_failed", "短链未返回受支持的视频重定向，请提供原始 BV 链接。")
            url = normalize_url(urljoin(url, location))
        except (OSError, http.client.HTTPException):
            fail("short_link_failed", "短链解析失败；未切换代理或绕过访问限制。")
        finally:
            connection.close()
    if urlsplit(url).hostname != "b23.tv":
        return normalize_url(url, int(original_part) if original_part else None)
    fail("short_link_failed", "短链重定向次数超过限制。")


def cookie_text(filename):
    """Read only an explicitly supplied Netscape file; retain Bilibili cookies only.

    yt-dlp gets a private in-memory stream, so it cannot rewrite the user's file.
    Never include this stream or parser errors in logs, receipts or output JSON.
    """
    if not filename:
        return None
    try:
        with Path(filename).open("rb") as handle:
            raw = handle.read(MAX_COOKIE_BYTES + 1)
        if len(raw) > MAX_COOKIE_BYTES:
            raise ValueError()
        lines = raw.decode("utf-8-sig").splitlines()
        if not lines or "HTTP Cookie File" not in lines[0]:
            raise ValueError()
        selected = ["# Netscape HTTP Cookie File"]
        for line in lines[1:]:
            if not line or (line.startswith("#") and not line.startswith("#HttpOnly_")):
                continue
            fields = line.removeprefix("#HttpOnly_").split("\t")
            if len(fields) != 7:
                raise ValueError()
            domain = fields[0].lstrip(".").lower()
            if domain == "bilibili.com" or domain.endswith(".bilibili.com"):
                selected.append(line)
        if len(selected) == 1:
            raise ValueError()
        return "\n".join(selected) + "\n"
    except (OSError, UnicodeError, ValueError):
        fail("invalid_cookies", "无法读取指定 Netscape Cookie 文件，或其中没有哔哩哔哩 Cookie；请由用户检查，不回显内容。")


class QuietLogger:
    # Retain only a boolean: upstream messages can contain secrets or signed URLs.
    def __init__(self):
        self.restriction_detected = False

    def debug(self, *_args, **_kwargs):
        pass

    info = error = debug

    def warning(self, message, *_args, **_kwargs):
        lowered = str(message).lower()
        signals = ("supporter-only", "only the preview", "geo-restricted", "rate limit",
                   "http error 403", "http error 412", "http error 429", "试看")
        if any(signal in lowered for signal in signals):
            self.restriction_detected = True


def doctor():
    try:
        version = importlib.metadata.version("yt-dlp")
    except importlib.metadata.PackageNotFoundError:
        version = None
    ready = sys.version_info >= (3, 11) and version is not None
    return {"ok": ready, "python": sys.version.split()[0], "yt_dlp": version,
            "ffmpeg": bool(shutil.which("ffmpeg")), "ffprobe": bool(shutil.which("ffprobe")),
            "download_ready": ready and bool(shutil.which("ffmpeg")) and bool(shutil.which("ffprobe")),
            "note": "仅检查依赖；不联网、不读取 Cookie、不连接学习系统。"}


def metadata(info, url, category="哔哩哔哩", tags=()):
    if not isinstance(info, dict) or info.get("_type", "video") != "video" or info.get("is_live"):
        fail("unsupported_result", "返回的不是单个点播视频，已停止，未展开合集或跳转其他解析器。")
    if info.get("availability") in ("premium_only", "subscriber_only", "needs_auth", "private"):
        fail("access_restricted", "原站提示访问权限受限，已停止，不将试看标记为完整视频。")
    match = re.fullmatch(r"(BV[0-9A-Za-z]{10})(?:_p([1-9][0-9]*))?", str(info.get("id", "")))
    if not match:
        fail("invalid_metadata", "解析结果缺少可核实的 BV 标识。")
    part = int(parse_qs(urlsplit(url).query)["p"][0])
    if int(match[2] or 1) != part:
        fail("invalid_metadata", "解析结果与请求的分 P 不一致。")
    requested_id = urlsplit(url).path.strip("/").split("/")[-1]
    if requested_id.startswith("BV") and requested_id != match[1]:
        fail("invalid_metadata", "解析结果与请求的 BV 标识不一致。")
    canonical = f"https://www.bilibili.com/video/{match[1]}/?p={part}"
    item = {"external_id": f"bilibili:{match[1]}:p{part}", "kind": "video",
            "title": text(info.get("title"), 500, "标题"), "url": canonical,
            "format": "text", "category": text(category, 200, "分类"),
            "tags": list(dict.fromkeys(["哔哩哔哩", *tags])),
            "summary": "原站视频信息，非视频内容摘要；未自动转写或生成讲解。"}
    if info.get("uploader"):
        item["author"] = text(info["uploader"], 200, "UP 主")
    timestamp = info.get("timestamp")
    if isinstance(timestamp, (int, float)) and not isinstance(timestamp, bool) and math.isfinite(timestamp):
        try:
            published = datetime.fromtimestamp(timestamp, timezone.utc)
            if published.year >= 1970:
                item["published_at"] = published.isoformat().replace("+00:00", "Z")
        except (OverflowError, OSError, ValueError):
            pass
    return item


def save_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, allow_nan=False, indent=2) + "\n", encoding="utf-8")


def save_subtitles(info, folder):
    """Bilibili supplies SRT data. Do not follow arbitrary subtitle URLs/danmaku."""
    saved = []
    for language, tracks in (info.get("subtitles") or {}).items():
        if not re.fullmatch(r"(?:ai-)?(?:zh|en)(?:[-_][A-Za-z0-9]+)*", language):
            continue
        for track in tracks:
            data, extension = track.get("data"), track.get("ext")
            if not isinstance(data, str) or not data.strip() or extension not in ("srt", "vtt"):
                continue
            if len(data.encode("utf-8")) > MAX_SUBTITLE_BYTES:
                fail("subtitle_too_large", "单个字幕超过 8 MiB，已停止保存。")
            filename = f"subtitles.{language}.{extension}"
            (folder / filename).write_text(data, encoding="utf-8")
            saved.append(filename)
            break
    return saved


def options(args, folder, cookies):
    sizes = {}
    limit = args.max_size_mib * 1024 * 1024

    def progress(event):
        name = event.get("filename", "media")
        downloaded = event.get("downloaded_bytes", 0)
        if isinstance(downloaded, (int, float)):
            sizes[name] = max(sizes.get(name, 0), downloaded)
            if sum(sizes.values()) > limit:
                fail("size_limit", "分离音视频累计下载量超过本次限制；部分文件可能保留，不自动重试。")

    return {"quiet": True, "no_warnings": True, "noprogress": True, "logger": QuietLogger(),
            "cachedir": False, "noplaylist": True, "playlist_items": "1",
            "socket_timeout": args.timeout, "retries": 0, "fragment_retries": 0, "extractor_retries": 0,
            "concurrent_fragment_downloads": 1, "sleep_interval_requests": 1,
            "cookiefile": io.StringIO(cookies) if cookies else None,
            "writesubtitles": args.subtitles, "writeautomaticsub": False,
            "paths": {"home": str(folder)}, "outtmpl": "video.%(ext)s", "overwrites": False,
            "format": f"bestvideo[ext=mp4][height<={args.height}]+bestaudio[ext=m4a]/best[ext=mp4][height<={args.height}]",
            "merge_output_format": "mp4", "max_filesize": limit, "progress_hooks": [progress]}


def load_backend():
    try:
        from yt_dlp import YoutubeDL
        from yt_dlp.extractor.bilibili import BiliBiliIE
        return YoutubeDL, BiliBiliIE
    except ImportError:
        fail("missing_dependency", "请在选定 Python 环境安装本 Skill 的 requirements.txt，再运行 doctor。")


def prepare(args):
    bounded(args.max_items, 1, 100, "max-items")
    bounded(args.timeout, 5, 300, "timeout")
    bounded(args.max_size_mib, 1, 2000, "max-size-mib")
    if args.part is not None:
        bounded(args.part, 1, 10000, "part")
    text(args.category, 200, "分类")
    if len(set(["哔哩哔哩", *args.tag])) > 20:
        fail("invalid_option", "包括默认标签在内最多 20 个标签。")
    for tag in args.tag:
        text(tag, 80, "标签")
    if len(args.urls) > args.max_items:
        fail("item_limit", "输入数量超过 max-items；请明确分批，不会截断或自动扩展范围。")
    return list(dict.fromkeys(normalize_url(url, args.part) for url in args.urls))


def collect(args, backend=None):
    targets = prepare(args)
    if args.dry_run:
        return {"ok": True, "dry_run": True, "network": False, "files_written": False,
                "targets": targets, "download": args.download, "subtitles": args.subtitles,
                "cookies_requested": bool(args.cookies_file),
                "note": "仅验证输入与范围；未核验视频是否存在、版权或实际可访问性。"}
    YoutubeDL, BiliBiliIE = backend or load_backend()
    if args.download and not (shutil.which("ffmpeg") and shutil.which("ffprobe")):
        fail("missing_ffmpeg", "下载合并需要 PATH 中的 FFmpeg 和 ffprobe，不会自动安装。")
    cookies = cookie_text(args.cookies_file)
    root = Path(args.output or (".artifacts/bilibili/" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ"))).resolve()
    try:
        root.mkdir(parents=True, exist_ok=False)
    except FileExistsError:
        fail("output_exists", "输出目录已经存在；请选择新的运行目录，避免覆盖既有成果。")
    items, seen = [], set()
    manifest = {"ok": False, "status": "running", "requested": len(targets), "records": [],
                "link_items": 0, "downloaded": 0, "skipped_duplicates": 0,
                "note": "仅本地采集；未连接或写入 WzStudy。"}

    def persist():
        manifest["link_items"] = len(items)
        manifest["downloaded"] = sum(bool(record.get("video")) for record in manifest["records"])
        save_json(root / "items.json", {"items": items})
        save_json(root / "manifest.json", manifest)

    persist()
    for number, target in enumerate(targets, 1):
        record = {"index": number, "url": target, "status": "running"}
        manifest["records"].append(record)
        folder = root / f"{number:03d}"
        try:
            target = resolve_short(target, args.timeout)
            record["url"] = target
            folder.mkdir()
            # Only this extractor is registered; process=False refuses URL/playlist expansion.
            with YoutubeDL(options(args, folder, cookies), auto_init=False) as ydl:
                ydl.add_info_extractor(BiliBiliIE())
                info = ydl.extract_info(target, download=False, process=False, ie_key="BiliBili")
                if ydl.params["logger"].restriction_detected:
                    fail("access_restricted", "原站提示试看、访问或频率限制，已停止，不将受限结果标记为完整视频。")
                item = metadata(info, target, args.category, args.tag)
                if item["external_id"] in seen:
                    record["status"] = "duplicate"
                    manifest["skipped_duplicates"] += 1
                    persist()
                    continue
                seen.add(item["external_id"])
                items.append(item)
                save_json(folder / "metadata.json", item)
                record.update({"external_id": item["external_id"], "title": item["title"],
                               "metadata": f"{number:03d}/metadata.json"})
                duration = info.get("duration")
                if isinstance(duration, (int, float)) and not isinstance(duration, bool) and math.isfinite(duration) and duration >= 0:
                    record["duration_seconds"] = duration
                if args.subtitles:
                    subtitles = save_subtitles(info, folder)
                    record["subtitles"] = [f"{number:03d}/{name}" for name in subtitles]
                    if not subtitles:
                        record["subtitle_note"] = "未取得支持的中英字幕；不代表视频没有字幕，也未自动语音识别。"
                if args.download:
                    # Already extracted and checked: never resolve a playlist or generic URL here.
                    ydl.params["writesubtitles"] = False
                    ydl.process_ie_result(info, download=True)
                    video = folder / "video.mp4"
                    if not video.is_file() or video.stat().st_size == 0:
                        fail("download_incomplete", "没有得到完整 MP4；可能超限、缺少可用格式或下载被限制，保留元数据及部分文件。")
                    if video.stat().st_size > args.max_size_mib * 1024 * 1024:
                        fail("size_limit", "合并文件超过本次限制；文件保留但未标记下载成功。")
                    record["video"] = f"{number:03d}/video.mp4"
                    record["video_bytes"] = video.stat().st_size
                record["status"] = "complete"
        except KeyboardInterrupt:
            record.update({"status": "interrupted", "error": "interrupted", "message": "用户中止；不自动重试。"})
            manifest["status"] = "interrupted"
            persist()
            return {**manifest, "output": str(root)}
        except Exception as error:
            # Never echo upstream exception text, cookie material or signed CDN URLs.
            code = error.code if isinstance(error, CollectError) else "collection_failed"
            message = error.message if isinstance(error, CollectError) else "采集失败；可能是网络、登录、版权/地区限制、风控或解析器变化。停止且不绕过，不自动重试。"
            record.update({"status": "failed", "error": code, "message": message})
            manifest["status"] = "partial" if items else "failed"
            persist()
            return {**manifest, "output": str(root)}
        persist()
    manifest.update({"ok": True, "status": "complete"})
    persist()
    return {**manifest, "output": str(root)}


def parser():
    result = argparse.ArgumentParser(description="哔哩哔哩定量采集：默认保存元数据/链接，不下载、不入库。")
    commands = result.add_subparsers(dest="command", required=True)
    commands.add_parser("doctor", help="只读、离线依赖检查")
    command = commands.add_parser("collect", help="采集用户提供的单视频或指定分 P")
    command.add_argument("urls", nargs="+", help="BV/av 号、单视频 URL 或 b23.tv 短链")
    command.add_argument("--output", help="新的输出目录；默认 .artifacts/bilibili/<UTC运行时间>")
    command.add_argument("--dry-run", action="store_true", help="不联网、不写文件、不读 Cookie")
    command.add_argument("--download", action="store_true", help="明确授权后才用：下载并合并 MP4")
    command.add_argument("--subtitles", action="store_true", help="尽力保存可访问的中英 SRT/VTT，不做语音转写")
    command.add_argument("--cookies-file", help="用户明确提供的 Netscape Cookie 文件，只读，禁止自动导出浏览器 Cookie")
    command.add_argument("--part", type=int, help="明确指定分 P；覆盖输入链接的 p，缺省每个链接的 p 或第 1 P")
    command.add_argument("--max-items", type=int, default=10, help="本批最多输入数，1–100，默认10；不自动发现视频")
    command.add_argument("--height", type=int, default=720, choices=(360, 480, 720, 1080, 1440, 2160))
    command.add_argument("--max-size-mib", type=int, default=200, help="单视频音视频累计下载及最终文件上限，默认200 MiB")
    command.add_argument("--timeout", type=int, default=30, help="单次网络请求超时秒数，5–300，不是整段视频时限")
    command.add_argument("--category", default="哔哩哔哩")
    command.add_argument("--tag", action="append", default=[])
    return result


def main(argv=None):
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    args = parser().parse_args(argv)
    try:
        answer = doctor() if args.command == "doctor" else collect(args)
    except CollectError as error:
        answer = {"ok": False, "error": error.code, "message": error.message}
    except OSError:
        answer = {"ok": False, "error": "local_io_error", "message": "本地文件操作失败；不回显路径或文件内容，请检查所选输出位置。"}
    print(json.dumps(answer, ensure_ascii=False, allow_nan=False, indent=2))
    return 0 if answer.get("ok") else 1


if __name__ == "__main__":
    raise SystemExit(main())
