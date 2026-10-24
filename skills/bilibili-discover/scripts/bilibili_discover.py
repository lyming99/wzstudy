#!/usr/bin/env python3
"""Bounded Bilibili discovery and verified local filtering. Python 3.11+.

Default: video data only, no downloads, ingestion, daemon or scheduler.
Unsigned ordinary requests only; no WBI/anti-bot bypass or fabricated device IDs.
An explicit JSON import can be filtered without any network access.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import csv
from datetime import date, datetime, time, timedelta, timezone
import html
import http.client
import http.cookiejar
import importlib.util
import io
import json
import math
import os
from pathlib import Path
import re
import sys
import tempfile
import time as clock
import unicodedata
from urllib.parse import urlencode
from urllib.request import Request
import uuid
import warnings

CHINA = timezone(timedelta(hours=8))
BV_PATTERN = r"BV[0-9A-Za-z]{10}"
API_PATHS = {"/x/web-interface/search/type", "/x/web-interface/newlist",
             "/x/web-interface/view", "/x/tag/archive/tags"}
DEFAULTS = {"keywords": [], "tags": [], "tids": [], "tid_v2s": [], "partition_names": [],
            "keyword_match": "all", "tag_match": "all", "keyword_fields": ["title", "description", "tags"],
            "date_from": None, "date_to": None, "lookback_days": None,
            "limit": 20, "max_pages": 5, "max_candidates": 100, "max_requests": 250,
            "interval_seconds": 2.0, "timeout": 30, "order": "pubdate"}
MAX_BODY = 8 * 1024 * 1024
MAX_INPUT = 16 * 1024 * 1024


class DiscoverError(Exception):
    def __init__(self, code, message):
        self.code, self.message = code, message
        super().__init__(message)


class BudgetReached(Exception):
    pass


class VideoUnavailable(Exception):
    pass


def fail(code, message):
    raise DiscoverError(code, message)


def integer(value, low, high, name):
    if isinstance(value, bool) or not isinstance(value, int) or not low <= value <= high:
        fail("invalid_option", f"{name} 必须是 {low}–{high} 的整数。")
    return value


def clean_text(value, byte_limit, name):
    if not isinstance(value, str) or not value.strip() or len(value.encode("utf-8")) > byte_limit:
        fail("invalid_option", f"{name} 必须非空且不超过 {byte_limit} UTF-8 字节。")
    if any(ord(c) < 32 or ord(c) == 127 for c in value):
        fail("invalid_option", f"{name} 不能包含控制字符。")
    return value.strip()


def text_list(value, limit, name, count=10):
    if not isinstance(value, list) or len(value) > count:
        fail("invalid_option", f"{name} 必须是最多 {count} 项的列表。")
    return list(dict.fromkeys(clean_text(v, limit, name) for v in value))


def fold(value):
    return unicodedata.normalize("NFKC", value).casefold()


def read_json(path, limit):
    try:
        with Path(path).open("rb") as handle:
            raw = handle.read(limit + 1)
        if len(raw) > limit:
            raise ValueError()
        return json.loads(raw.decode("utf-8-sig"), parse_constant=lambda _: fail("invalid_json", "JSON 不能包含非有限数值。"))
    except (OSError, UnicodeError, ValueError):
        fail("invalid_json", "无法读取指定的 UTF-8 JSON，或文件超过大小限制；不回显内容。")


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, allow_nan=False, indent=2) + "\n", encoding="utf-8")


def load_collector():
    path = Path(__file__).resolve().parents[2] / "bilibili-collect/scripts/bilibili_collect.py"
    if not path.is_file():
        fail("missing_collector", "需要同项目的 bilibili-collect Skill；请保留两个 Skill 的相邻目录结构。")
    spec = importlib.util.spec_from_file_location("wzstudy_bilibili_collect_bridge", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def prepare(args, now=None):
    rules = dict(DEFAULTS)
    if args.config:
        config = read_json(args.config, 128 * 1024)
        if not isinstance(config, dict) or set(config) - set(DEFAULTS):
            fail("invalid_config", "配置仅允许文档列出的筛选和预算字段；不允许凭据、下载、路径或执行命令。")
        rules.update(config)
    if args.lookback_days is not None and (args.date_from is not None or args.date_to is not None):
        fail("invalid_dates", "命令行不能同时指定固定日期和 --last-days。")
    for key in DEFAULTS:
        value = getattr(args, key, None)
        if value is not None:
            rules[key] = value
    if args.lookback_days is not None:
        rules["date_from"] = rules["date_to"] = None
    elif args.date_from is not None or args.date_to is not None:
        rules["lookback_days"] = None
    integer(args.max_size_mib, 1, 2000, "max-size-mib")
    for key in ("keywords", "tags", "partition_names"):
        rules[key] = text_list(rules[key], 200 if key == "partition_names" else 80, key)
    for key in ("tids", "tid_v2s"):
        if not isinstance(rules[key], list) or len(rules[key]) > 10:
            fail("invalid_option", f"{key} 必须是最多 10 个数字 ID 的列表。")
        rules[key] = list(dict.fromkeys(integer(v, 1, 1000000, key) for v in rules[key]))
    if rules["tids"] and rules["tid_v2s"]:
        fail("ambiguous_partition", "旧 tid 与新 tid_v2 不能混用；请明确使用一套分区编号。")
    for key in ("keyword_match", "tag_match"):
        if rules[key] not in ("all", "any"):
            fail("invalid_option", f"{key} 只支持 all 或 any。")
    fields = rules["keyword_fields"]
    if not isinstance(fields, list) or not fields or len(fields) > 3 or any(v not in ("title", "description", "tags") for v in fields):
        fail("invalid_option", "keyword_fields 只支持 title、description、tags 的非空列表。")
    rules["keyword_fields"] = list(dict.fromkeys(fields))
    for key, low, high in (("limit", 1, 100), ("max_pages", 1, 50), ("max_candidates", 1, 1000),
                           ("max_requests", 1, 500), ("timeout", 5, 120)):
        integer(rules[key], low, high, key)
    interval = rules["interval_seconds"]
    if isinstance(interval, bool) or not isinstance(interval, (int, float)) or not math.isfinite(interval) or not 1 <= interval <= 60:
        fail("invalid_option", "interval_seconds 必须是 1–60 秒；不支持无间隔高并发。")
    if rules["order"] not in ("pubdate", "totalrank", "click", "stow"):
        fail("invalid_option", "order 只支持 pubdate/totalrank/click/stow。")
    today = (now or datetime.now(CHINA)).astimezone(CHINA).date()
    try:
        if rules["date_from"] is not None or rules["date_to"] is not None:
            if rules["lookback_days"] is not None:
                fail("invalid_dates", "固定日期与 lookback_days 不能同时设置。")
            if not all(isinstance(rules[k], str) and re.fullmatch(r"\d{4}-\d{2}-\d{2}", rules[k]) for k in ("date_from", "date_to")):
                raise ValueError()
            first, last = date.fromisoformat(rules["date_from"]), date.fromisoformat(rules["date_to"])
        else:
            days = integer(rules["lookback_days"] if rules["lookback_days"] is not None else 7, 1, 366, "lookback_days")
            first, last = today - timedelta(days=days - 1), today
        if first.year < 1970 or last < first or (last - first).days > 3660:
            raise ValueError()
        end_day = last + timedelta(days=1)
        rules["date_from"], rules["date_to"] = first.isoformat(), last.isoformat()
        rules["begin_timestamp"] = int(datetime.combine(first, time(), CHINA).timestamp())
        rules["end_timestamp_exclusive"] = int(datetime.combine(end_day, time(), CHINA).timestamp())
    except (ValueError, OverflowError, TypeError):
        fail("invalid_dates", "请同时提供 YYYY-MM-DD 起止日期，开始不晚于结束；跨度最多 3661 个自然日。")
    rules["timezone"] = "Asia/Shanghai (UTC+08:00)"
    rules["lookback_days"] = None  # Resolved exact dates are recorded in every run.
    if not args.input and not any(rules[k] for k in ("keywords", "tags", "tids")):
        fail("missing_seed", "在线发现至少需要关键词、tag 或旧 tid；仅新 tid_v2/分区名称/日期时，请补充关键词/tag 或导入候选 JSON。")
    if args.refresh and not args.state:
        fail("invalid_option", "--refresh 需要同时指定 --state。")
    return rules


def jobs_for(rules):
    if rules["keywords"]:
        terms = rules["keywords"] if rules["keyword_match"] == "any" else [" ".join(rules["keywords"])]
    elif rules["tags"]:
        terms = rules["tags"] if rules["tag_match"] == "any" else rules["tags"][:1]
    else:
        terms = []
    jobs = []
    if terms:
        for query in terms:
            for tid in rules["tids"] or [0]:
                jobs.append({"mode": "search", "query": query, "tid": tid, "page": 1, "done": False})
    else:
        jobs = [{"mode": "region", "tid": tid, "page": 1, "done": False} for tid in rules["tids"]]
    if len(jobs) > 50:
        fail("too_many_queries", "关键词与分区组合超过 50 组，请缩小本次范围。")
    return jobs


class ApiClient:
    def __init__(self, rules, cookies_file=None, collector=None, sleeper=clock.sleep, timer=clock.monotonic, connection=None):
        self.rules, self.requests, self.last_request = rules, 0, None
        self.sleeper, self.timer = sleeper, timer
        self.connection = connection or http.client.HTTPSConnection
        self.jar = http.cookiejar.MozillaCookieJar()
        if cookies_file:
            try:
                content = collector.cookie_text(cookies_file)
                with warnings.catch_warnings():
                    warnings.simplefilter("ignore")
                    self.jar._really_load(io.StringIO(content), "<memory>", True, False)
            except Exception:
                fail("invalid_cookies", "无法使用指定的 Netscape Cookie 文件；不打印内容、不修改原文件。")

    def _get(self, host, path, as_json=True):
        if host not in ("api.bilibili.com", "www.bilibili.com") or not path.startswith("/"):
            fail("invalid_endpoint", "拒绝访问非固定 Bilibili 主机。")
        if self.requests >= self.rules["max_requests"]:
            raise BudgetReached()
        if self.last_request is not None:
            wait = self.rules["interval_seconds"] - (self.timer() - self.last_request)
            if wait > 0:
                self.sleeper(wait)
        self.last_request = self.timer()
        self.requests += 1
        request = Request("https://" + host + path)
        self.jar.add_cookie_header(request)
        headers = {"User-Agent": "Mozilla/5.0 (compatible; WzStudy-bilibili-discover/1.0)",
                   "Referer": "https://www.bilibili.com/", "Accept": "application/json" if as_json else "text/html",
                   "Accept-Encoding": "identity"}
        if request.has_header("Cookie"):
            headers["Cookie"] = request.get_header("Cookie")
        connection = self.connection(host, timeout=self.rules["timeout"])
        try:
            connection.request("GET", path, headers=headers)
            response = connection.getresponse()
            status = response.status
            if status in (401, 403, 412, 429):
                fail("access_restricted", "原站拒绝访问或触发频率/风控限制；停止，不自动重试或伪造标识。")
            if 300 <= status < 400:
                fail("redirect_refused", "拒绝跟随接口重定向，未把 Cookie 转发给其他地址。")
            if not 200 <= status < 300:
                fail("upstream_http_error", f"原站 HTTP 状态 {status}；不回显响应正文。")
            with warnings.catch_warnings():
                warnings.simplefilter("ignore")
                self.jar.extract_cookies(response, request)
            for cookie in list(self.jar):
                domain = cookie.domain.lstrip(".").lower()
                if not (domain == "bilibili.com" or domain.endswith(".bilibili.com")):
                    self.jar.clear(cookie.domain, cookie.path, cookie.name)
            if not as_json:
                return None  # Homepage is used only for ordinary anonymous Set-Cookie headers.
            raw = response.read(MAX_BODY + 1)
            if len(raw) > MAX_BODY:
                fail("response_too_large", "原站单次 JSON 响应超过 8 MiB。")
        except (OSError, http.client.HTTPException):
            fail("network_error", "原站网络请求失败；未重试，不回显连接或响应中的敏感信息。")
        finally:
            connection.close()
        try:
            value = json.loads(raw, parse_constant=lambda _: fail("invalid_response", "原站返回非有限 JSON 数值。"))
        except (ValueError, UnicodeError):
            fail("invalid_response", "原站没有返回有效 JSON；可能要求登录、签名或验证码，已停止。")
        if not isinstance(value, dict) or isinstance(value.get("code"), bool) or not isinstance(value.get("code"), int):
            fail("invalid_response", "接口响应缺少可核实的状态码。")
        if value["code"] in (-404, 62002):
            raise VideoUnavailable()
        if value["code"] != 0:
            fail("upstream_denied", "原站接口拒绝请求；不尝试签名绕过、切换接口或账号。")
        data = value.get("data")
        if value.get("v_voucher") or (isinstance(data, dict) and data.get("v_voucher")):
            fail("access_restricted", "原站要求风控验证，已停止。")
        return data

    def bootstrap(self):
        if not any(c.name == "buvid3" and not c.is_expired() for c in self.jar):
            self._get("www.bilibili.com", "/", as_json=False)

    def json(self, path, params):
        if path not in API_PATHS:
            fail("invalid_endpoint", "仅允许固定的搜索、分区、详情和标签请求。")
        return self._get("api.bilibili.com", path + "?" + urlencode(params))


def nonnegative(value):
    return value if isinstance(value, int) and not isinstance(value, bool) and 0 <= value <= 2**63 - 1 else None


def timestamp_of(value):
    if nonnegative(value) is not None:
        return value
    if isinstance(value, str):
        try:
            parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
            if parsed.tzinfo is not None:
                return int(parsed.timestamp())
        except (ValueError, OverflowError, OSError):
            pass
    return None


def normalize_video(detail, tags, expected_bvid, source):
    if not isinstance(detail, dict) or detail.get("bvid") != expected_bvid:
        fail("identity_mismatch", "视频详情的 BV 标识缺失或与候选不一致；已停止。")
    title = detail.get("title")
    pubdate = timestamp_of(detail.get("pubdate", detail.get("published_at")))
    if not isinstance(title, str) or not title.strip() or len(title.encode("utf-8")) > 500 or pubdate is None:
        return None, "unverified_metadata"
    try:
        published = datetime.fromtimestamp(pubdate, CHINA).isoformat()
    except (ValueError, OverflowError, OSError):
        return None, "unverified_metadata"
    rights = detail.get("rights") or {}
    if not isinstance(rights, dict):
        return None, "unverified_metadata"
    if any(v is True or v == 1 for v in (detail.get("is_upower_exclusive"), rights.get("is_upower_exclusive"),
                                        rights.get("ugc_pay"), rights.get("is_chargeable_season"))):
        return None, "restricted_video"
    owner = detail.get("owner") or {}
    if not isinstance(owner, dict):
        owner = {}
    stat = detail.get("stat", detail.get("stats")) or {}
    if not isinstance(stat, dict):
        stat = {}
    names = None
    if tags is not None:
        if not isinstance(tags, list) or len(tags) > 200:
            fail("invalid_response", "标签响应不是支持的列表。")
        names = []
        for entry in tags:
            if isinstance(entry, dict):
                if entry.get("tag_type") == "bgm":
                    continue
                name = entry.get("tag_name", entry.get("name"))
            else:
                name = entry
            if not isinstance(name, str) or not name.strip() or len(name.encode("utf-8")) > 256:
                fail("invalid_response", "标签缺少可核实名称或超过长度限制。")
            if name not in names:
                names.append(name.strip())
    duration = detail.get("duration", detail.get("total_duration_seconds"))
    if isinstance(duration, bool) or not isinstance(duration, (int, float)) or not math.isfinite(duration) or duration < 0:
        duration = None
    def optional_name(value):
        return value.strip() if isinstance(value, str) and len(value.encode("utf-8")) <= 200 else None
    author = optional_name(owner.get("name", detail.get("author")))
    record = {"bvid": expected_bvid, "aid": nonnegative(detail.get("aid")),
              "external_id": f"bilibili:{expected_bvid}:p1", "title": title.strip(),
              "url": f"https://www.bilibili.com/video/{expected_bvid}/?p=1",
              "author": author, "author_id": nonnegative(owner.get("mid", detail.get("mid", detail.get("author_id")))),
              "published_at": published, "pubdate": pubdate,
              "tid": nonnegative(detail.get("tid")), "tname": optional_name(detail.get("tname")),
              "tid_v2": nonnegative(detail.get("tid_v2")), "tname_v2": optional_name(detail.get("tname_v2")),
              "tags": names, "total_duration_seconds": duration,
              "part_count": len(detail["pages"]) if isinstance(detail.get("pages"), list) else nonnegative(detail.get("part_count")),
              "stats": {k: nonnegative(stat.get(k)) for k in ("view", "like", "coin", "favorite", "share", "reply", "danmaku")},
              "verification_source": source}
    description = detail.get("desc", detail.get("description", ""))
    if not isinstance(description, str) or len(description.encode("utf-8")) > 262144:
        description = ""
    # Description is transient for keyword matching; never exported as a claimed transcript.
    return (record, description), None


def matches(record, description, rules):
    if not rules["begin_timestamp"] <= record["pubdate"] < rules["end_timestamp_exclusive"]:
        return "date_mismatch"
    for field, key in (("tid", "tids"), ("tid_v2", "tid_v2s")):
        if rules[key]:
            if record[field] is None:
                return "unverified_partition"
            if record[field] not in rules[key]:
                return "partition_mismatch"
    if rules["partition_names"]:
        names = {fold(v) for v in (record["tname"], record["tname_v2"]) if v}
        if not names:
            return "unverified_partition"
        if not names.intersection(fold(v) for v in rules["partition_names"]):
            return "partition_mismatch"
    actual_tags = {fold(v) for v in record["tags"] or []}
    if rules["tags"]:
        if record["tags"] is None:
            return "unverified_tags"
        tests = [fold(tag) in actual_tags for tag in rules["tags"]]
        if not (all(tests) if rules["tag_match"] == "all" else any(tests)):
            return "tag_mismatch"
    if rules["keywords"]:
        fields = {"title": record["title"], "description": description, "tags": "\n".join(record["tags"] or [])}
        haystack = fold(html.unescape("\n".join(fields[key] for key in rules["keyword_fields"])))
        tests = [fold(term) in haystack for term in rules["keywords"]]
        if not (all(tests) if rules["keyword_match"] == "all" else any(tests)):
            return "keyword_mismatch"
    return None


def content_item(record, collector):
    tags = [v for v in record["tags"] or [] if len(v.encode("utf-8")) <= 80 and v != "哔哩哔哩"][:19]
    item = collector.metadata({"id": record["bvid"], "title": record["title"], "uploader": record["author"],
                               "timestamp": record["pubdate"]}, record["url"],
                              category=record["tname_v2"] or record["tname"] or "哔哩哔哩", tags=tags)
    return item


def spreadsheet_text(value):
    value = "" if value is None else str(value)
    return "'" + value if value.lstrip().startswith(("=", "+", "-", "@")) or value.startswith(("\t", "\r", "\n")) else value


def export_csv(path, records):
    fields = ["bvid", "title", "author", "author_id", "published_at", "tid", "tname", "tid_v2", "tname_v2",
              "tags", "total_duration_seconds", "view", "like", "coin", "favorite", "share", "reply", "danmaku", "url"]
    with path.open("w", encoding="utf-8-sig", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=fields)
        writer.writeheader()
        for record in records:
            row = {key: record.get(key) for key in fields}
            row.update(record["stats"])
            row["tags"] = " | ".join(record["tags"]) if record["tags"] is not None else ""
            writer.writerow({key: spreadsheet_text(row.get(key)) for key in fields})


@contextmanager
def state_session(filename, output, config=None):
    if not filename:
        yield {"schema_version": 1, "seen": {}}, None
        return
    path = Path(filename).absolute()
    if path.is_symlink() or path.resolve().is_relative_to(output.resolve()) or (config and path.resolve() == Path(config).resolve()):
        fail("invalid_state_path", "状态文件必须在运行输出目录外，且不能是配置文件或符号链接。")
    path.parent.mkdir(parents=True, exist_ok=True)
    lock = path.with_name(path.name + ".lock")
    token = uuid.uuid4().hex
    try:
        with lock.open("x", encoding="ascii") as handle:
            handle.write(token)
    except FileExistsError:
        fail("state_locked", "状态文件已被另一个任务锁定；不并发执行，不自动删除未知锁。")
    try:
        state = read_json(path, 5 * 1024 * 1024) if path.exists() else {"schema_version": 1, "seen": {}}
        if not isinstance(state, dict) or set(state) != {"schema_version", "seen"} or type(state["schema_version"]) is not int or state["schema_version"] != 1 or not isinstance(state["seen"], dict):
            fail("invalid_state", "已有状态文件不符合本 Skill 格式，不覆盖。")
        if len(state["seen"]) > 50000 or any(not re.fullmatch(BV_PATTERN, key) or not isinstance(value, str) or len(value) > 64 for key, value in state["seen"].items()):
            fail("invalid_state", "状态文件超过 50000 项或含非法记录，不覆盖。")
        yield state, path
    finally:
        try:
            if lock.read_text(encoding="ascii") == token:
                lock.unlink()
        except OSError:
            pass


def commit_state(path, state):
    if path is None:
        return
    if len(state["seen"]) > 50000:
        fail("state_capacity", "状态将超过 50000 项，未写入；请显式选择新状态文件，不自动淘汰去重记录。")
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, prefix=path.name + ".", suffix=".tmp", delete=False) as handle:
            temporary = Path(handle.name)
            json.dump(state, handle, ensure_ascii=False, allow_nan=False, indent=2)
            handle.write("\n")
        os.replace(temporary, path)
        temporary = None  # Nothing after a successful commit may mask that it committed.
    finally:
        if temporary:
            try:
                temporary.unlink(missing_ok=True)
            except OSError:
                pass


def fetch_page(client, job, rules):
    if job["mode"] == "search":
        data = client.json("/x/web-interface/search/type", {"search_type": "video", "keyword": job["query"],
                           "page": job["page"], "page_size": 20, "order": rules["order"], "tids": job["tid"],
                           "pubtime_begin": rules["begin_timestamp"], "pubtime_end": rules["end_timestamp_exclusive"] - 1,
                           "duration": 0, "highlight": 0})
        if not isinstance(data, dict):
            fail("invalid_response", "搜索响应缺少数据对象。")
        rows = data.get("result")
        if rows is None and data.get("numResults") == 0:
            rows = []
        pages = nonnegative(data.get("numPages"))
    else:
        data = client.json("/x/web-interface/newlist", {"rid": job["tid"], "type": 1, "ps": 20, "pn": job["page"]})
        if not isinstance(data, dict):
            fail("invalid_response", "分区响应缺少数据对象。")
        rows = data.get("archives")
        page = data.get("page") or {}
        if not isinstance(page, dict):
            fail("invalid_response", "分区分页字段不符合预期。")
        count, size = nonnegative(page.get("count")), nonnegative(page.get("size"))
        pages = math.ceil(count / size) if count is not None and size else None
    if not isinstance(rows, list) or len(rows) > 100:
        fail("invalid_response", "候选视频响应不是支持的有界列表。")
    if not rows or (pages is not None and job["page"] >= pages):
        job["done"] = True
    job["page"] += 1
    return rows


def run(args, now=None, client_factory=ApiClient, collector=None):
    rules = prepare(args, now)
    jobs = [] if args.input else jobs_for(rules)
    if args.dry_run:
        return {"ok": True, "dry_run": True, "network": False, "files_written": False,
                "rules": rules, "jobs": jobs, "mode": "import" if args.input else "online",
                "download": args.download, "subtitles": args.subtitles, "state_requested": bool(args.state),
                "note": "仅校验配置/条件与预算；未读取候选、Cookie 或状态，不证明原站可访问或结果完备。"}
    collector = collector or load_collector()
    root = Path(args.output or (".artifacts/bilibili-discover/" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ"))).resolve()
    if root.exists():
        fail("output_exists", "输出目录已经存在，请选择新的运行目录；不覆盖旧成果。")
    with state_session(args.state, root, args.config) as (state, state_path):
        root.mkdir(parents=True, exist_ok=False)
        videos, items, audited, local_seen = [], [], [], set()
        manifest = {"schema_version": 1, "ok": False, "status": "running", "rules": rules,
                    "mode": "import" if args.input else "online", "coverage": "bounded_not_exhaustive",
                    "matched": 0, "examined": 0, "api_requests": 0, "pages": 0, "skipped_seen": 0,
                    "truncated": False, "stop_reason": None, "rejections": {}, "jobs": jobs,
                    "state_committed": False, "requested_actions": {"download": args.download, "subtitles": args.subtitles},
                    "note": "数据采集不等于媒体下载/入库/发布；不含后台调度。"}
        client = None

        def persist():
            manifest["matched"], manifest["examined"] = len(videos), len(audited)
            manifest["api_requests"] = client.requests if client else 0
            write_json(root / "videos.json", {"schema_version": 1, "videos": videos})
            write_json(root / "items.json", {"items": items})
            write_json(root / "candidates.json", {"candidates": audited})
            (root / "urls.txt").write_text("".join(record["url"] + "\n" for record in videos), encoding="utf-8")
            export_csv(root / "videos.csv", videos)
            write_json(root / "manifest.json", manifest)

        def reject(entry, reason):
            entry.update({"decision": "rejected", "reason": reason})
            manifest["rejections"][reason] = manifest["rejections"].get(reason, 0) + 1

        def consider(candidate, imported=False):
            if len(audited) >= rules["max_candidates"]:
                manifest.update({"truncated": True, "stop_reason": "max_candidates"})
                return False
            bvid = candidate.get("bvid") if isinstance(candidate, dict) else None
            entry = {"bvid": bvid if isinstance(bvid, str) and re.fullmatch(BV_PATTERN, bvid) else None}
            audited.append(entry)
            if entry["bvid"] is None:
                reject(entry, "unverified_candidate")
                return True
            if bvid in local_seen:
                entry["decision"] = "duplicate"
                return True
            local_seen.add(bvid)
            if bvid in state["seen"] and not args.refresh:
                entry["decision"] = "seen"
                manifest["skipped_seen"] += 1
                return True
            try:
                detail = candidate if imported else client.json("/x/web-interface/view", {"bvid": bvid})
                tags = candidate.get("tags") if imported else client.json("/x/tag/archive/tags", {"bvid": bvid})
            except VideoUnavailable:
                reject(entry, "unavailable_video")
                return True
            normalized, reason = normalize_video(detail, tags, bvid, "user_import" if imported else "bilibili_detail_and_tags")
            if reason:
                reject(entry, reason)
                return True
            record, description = normalized
            reason = matches(record, description, rules)
            if reason:
                reject(entry, reason)
                return True
            record["collected_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
            items.append(content_item(record, collector))
            videos.append(record)
            entry["decision"] = "matched"
            if len(videos) >= rules["limit"]:
                manifest.update({"truncated": True, "stop_reason": "limit"})
                return False
            return True

        persist()
        try:
            if args.input:
                data = read_json(args.input, MAX_INPUT)
                rows = data.get("videos") if isinstance(data, dict) else data
                if not isinstance(rows, list) or len(rows) > 10000:
                    fail("invalid_input", "导入需要视频详情对象数组或 {videos:[...]}，最多 10000 项、16 MiB。")
                for candidate in rows:
                    if not consider(candidate, imported=True):
                        break
            else:
                client = client_factory(rules, cookies_file=args.cookies_file, collector=collector)
                client.bootstrap()
                while any(not job["done"] for job in jobs) and manifest["pages"] < rules["max_pages"]:
                    stopped = False
                    for job in jobs:
                        if job["done"]:
                            continue
                        if manifest["pages"] >= rules["max_pages"]:
                            break
                        rows = fetch_page(client, job, rules)
                        manifest["pages"] += 1
                        for candidate in rows:
                            if not consider(candidate):
                                stopped = True
                                break
                        persist()
                        if stopped:
                            break
                    if stopped:
                        break
                if not manifest["stop_reason"] and any(not job["done"] for job in jobs):
                    manifest.update({"truncated": True, "stop_reason": "max_pages"})
            manifest["stop_reason"] = manifest["stop_reason"] or "candidate_sources_exhausted"
        except BudgetReached:
            for entry in audited:
                if "decision" not in entry:
                    entry.update({"decision": "unverified", "reason": "request_budget"})
            manifest.update({"truncated": True, "stop_reason": "max_requests"})
        except KeyboardInterrupt:
            for entry in audited:
                if "decision" not in entry:
                    entry.update({"decision": "unverified", "reason": "interrupted"})
            manifest.update({"status": "interrupted", "stop_reason": "interrupted", "error": "interrupted"})
            persist()
            return {**manifest, "output": str(root)}
        except Exception as error:
            code = error.code if isinstance(error, DiscoverError) else "discovery_failed"
            message = error.message if isinstance(error, DiscoverError) else "发现/筛选失败，已停止；不回显原始异常、Cookie 或接口响应。"
            for entry in audited:
                if "decision" not in entry:
                    entry.update({"decision": "unverified", "reason": code})
            manifest.update({"status": "partial" if videos else "failed", "stop_reason": "error", "error": code, "message": message})
            persist()
            return {**manifest, "output": str(root)}
        manifest.update({"ok": False, "status": "finalizing"})
        if not videos and (args.download or args.subtitles):
            manifest["media"] = {"status": "not_run", "downloaded": 0, "link_items": 0, "reason": "no_new_matches"}
            manifest["media_note"] = "没有新的匹配条目，未执行媒体任务；已有数据若需追加媒体，请核对旧回执后显式 --refresh 或选择独立状态文件。"
        persist()
        try:
            if videos and (args.download or args.subtitles):
                # This is the only media execution path; neither a task JSON nor source text can enable it.
                forward = ["collect", *[v["url"] for v in videos], "--output", str(root / "media"),
                           "--max-items", str(rules["limit"]), "--height", str(args.height),
                           "--max-size-mib", str(args.max_size_mib), "--timeout", str(rules["timeout"])]
                if args.cookies_file:
                    forward.extend(["--cookies-file", args.cookies_file])
                if args.download:
                    forward.append("--download")
                if args.subtitles:
                    forward.append("--subtitles")
                media = collector.collect(collector.parser().parse_args(forward))
                manifest["media"] = {"ok": media.get("ok", False), "downloaded": media.get("downloaded", 0),
                                     "link_items": media.get("link_items", 0), "manifest": "media/manifest.json"}
                if not media.get("ok"):
                    fail("media_failed", "条件发现已完成，但媒体采集未全部完成；保留回执，不提交去重状态、不自动重传。")
            for record in videos:
                state["seen"][record["bvid"]] = record["collected_at"]
            commit_state(state_path, state)
            manifest["state_committed"] = state_path is not None
            manifest.update({"ok": True, "status": "complete"})
        except KeyboardInterrupt:
            manifest.update({"ok": False, "status": "interrupted", "error": "interrupted", "stop_reason": "interrupted"})
        except Exception as error:
            manifest.update({"ok": False, "status": "partial", "error": error.code if isinstance(error, DiscoverError) else "finalize_failed",
                             "message": error.message if isinstance(error, DiscoverError) else "媒体衔接或状态保存失败，未声称全部完成。"})
        try:
            persist()
        except (OSError, UnicodeError):
            manifest.update({"ok": False, "status": "partial", "error": "receipt_write_failed",
                             "message": "最终本地回执写入失败；返回值保留真实 state_committed。数据/媒体可能已保存，先核对输出与状态，不自动重跑。"})
        return {**manifest, "output": str(root)}


def parser():
    result = argparse.ArgumentParser(description="按分区/tag/关键词/北京时间日期范围，定量发现并导出 B 站视频数据。")
    commands = result.add_subparsers(dest="command", required=True)
    commands.add_parser("doctor", help="只读离线检查，不联网或读取 Cookie")
    command = commands.add_parser("run")
    command.add_argument("--config", help="可复用的筛选/预算 JSON；不接受凭据或执行动作")
    command.add_argument("--keyword", dest="keywords", action="append")
    command.add_argument("--tag", dest="tags", action="append", help="真实标签精确匹配，不是标题包含")
    command.add_argument("--tid", dest="tids", action="append", type=int, help="旧分区数字 ID；多项为 OR")
    command.add_argument("--tid-v2", dest="tid_v2s", action="append", type=int, help="新分区数字 ID；需关键词/tag 提供候选")
    command.add_argument("--partition-name", dest="partition_names", action="append", help="详情分区名称精确匹配，不自动展开子分区")
    command.add_argument("--keyword-match", choices=("all", "any"))
    command.add_argument("--tag-match", choices=("all", "any"))
    command.add_argument("--keyword-fields", nargs="+", choices=("title", "description", "tags"))
    command.add_argument("--date-from", "--start-date", dest="date_from")
    command.add_argument("--date-to", "--end-date", dest="date_to")
    command.add_argument("--last-days", dest="lookback_days", type=int, help="含今天的最近 N 个北京时间自然日；默认7")
    for name in ("limit", "max_pages", "max_candidates", "max_requests", "timeout"):
        command.add_argument("--" + name.replace("_", "-"), type=int)
    command.add_argument("--interval", dest="interval_seconds", type=float, help="请求间隔秒数，默认2，最少1")
    command.add_argument("--order", choices=("pubdate", "totalrank", "click", "stow"))
    command.add_argument("--input", help="用户已获授权的视频详情 JSON；默认本地筛选，不访问原站")
    command.add_argument("--output", help="新的运行目录，默认 .artifacts/bilibili-discover/<UTC时间>")
    command.add_argument("--state", help="跨次去重文件，必须在运行目录外；共享状态按 BV 去重")
    command.add_argument("--refresh", action="store_true", help="明确忽略已采集标记，重新输出当前统计；需要 --state")
    command.add_argument("--dry-run", action="store_true", help="离线校验规则；不读取候选、Cookie 或状态、不写文件")
    command.add_argument("--cookies-file", help="仅用户明确提供的 Netscape 文件，不自动导出浏览器 Cookie")
    command.add_argument("--download", action="store_true", help="独立授权后才用：交给 bilibili-collect 下载匹配视频第1P")
    command.add_argument("--subtitles", action="store_true", help="独立授权后才用：保存匹配视频可访问的字幕，不做ASR")
    command.add_argument("--height", type=int, default=720, choices=(360, 480, 720, 1080, 1440, 2160))
    command.add_argument("--max-size-mib", type=int, default=200)
    return result


def main(argv=None):
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    args = parser().parse_args(argv)
    try:
        if args.command == "doctor":
            bridge = load_collector()
            answer = {"ok": sys.version_info >= (3, 11), "python": sys.version.split()[0],
                      "data_dependencies": "standard_library", "collector": bridge.doctor(),
                      "note": "数据筛选不要求 yt-dlp；媒体衔接才需要。未联网、未读取凭据、未启动定时任务。"}
        else:
            integer(args.max_size_mib, 1, 2000, "max-size-mib")
            answer = run(args)
    except DiscoverError as error:
        answer = {"ok": False, "error": error.code, "message": error.message}
    except OSError:
        answer = {"ok": False, "error": "local_io_error", "message": "本地文件操作失败；请检查所选路径，不回显文件内容。"}
    print(json.dumps(answer, ensure_ascii=False, allow_nan=False, indent=2))
    return 0 if answer.get("ok") else 1


if __name__ == "__main__":
    raise SystemExit(main())
