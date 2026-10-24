"""Synthetic-only tests. No real Bilibili requests, cookies or WzStudy writes."""
from contextlib import redirect_stderr, redirect_stdout
from copy import deepcopy
from datetime import datetime, timezone
from email.message import Message
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

SKILL = Path(__file__).resolve().parents[1]
ROOT = SKILL.parents[1]


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


d = load(SKILL / "scripts/bilibili_discover.py", "discover_test_subject")
collector = d.load_collector()
push = load(ROOT / "skills/wzstudy-push/scripts/wzstudy_push.py", "discover_push_contract")
BV0, BV1, BV2 = "BV0000000000", "BV1111111111", "BV2222222222"
STAMP = int(datetime(2026, 10, 1, 12, tzinfo=d.CHINA).timestamp())


def video(bvid=BV0, **updates):
    value = {"bvid": bvid, "aid": 123, "title": "Python 教程", "pubdate": STAMP,
             "owner": {"name": "合成作者", "mid": 123}, "tid": 201, "tname": "合成分区",
             "tid_v2": 10001, "tname_v2": "合成新版分区", "tags": ["Python", "教程"],
             "duration": 120, "pages": [{"page": 1}, {"page": 2}],
             "desc": "只供筛选、不应复制的简介：合成数据", "stat": {"view": 10, "like": 1, "reply": -1},
             "http_headers": {"Cookie": "synthetic-secret"}, "url": "https://evil.invalid/never-request"}
    value.update(updates)
    return value


class FakeApi:
    pages = {}
    details = {}
    tag_values = {}
    instances = []
    error_at = None
    error_value = None

    def __init__(self, rules, **_kwargs):
        self.rules, self.requests, self.calls = rules, 0, []
        self.instances.append(self)

    def count(self):
        if self.requests >= self.rules["max_requests"]:
            raise d.BudgetReached()
        self.requests += 1
        if self.error_at == self.requests:
            raise self.error_value

    def bootstrap(self):
        self.count()

    def json(self, path, params):
        self.count()
        self.calls.append((path, params))
        if path.endswith("search/type"):
            return deepcopy(self.pages.get((params["keyword"], params["tids"], params["page"]), {"result": [], "numPages": 0}))
        if path.endswith("newlist"):
            return deepcopy(self.pages.get((params["rid"], params["pn"]), {"archives": [], "page": {"count": 0, "size": 20}}))
        if path.endswith("/view"):
            return deepcopy(self.details.get(params["bvid"], video(params["bvid"])))
        if path.endswith("/tags"):
            return deepcopy(self.tag_values.get(params["bvid"], ["Python", "教程"]))
        raise AssertionError("Unexpected endpoint")


class BaseTests(unittest.TestCase):
    def setUp(self):
        (ROOT / ".artifacts").mkdir(exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix="bilibili-discover-test-", dir=ROOT / ".artifacts")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        FakeApi.pages, FakeApi.details, FakeApi.tag_values, FakeApi.instances = {}, {}, {}, []
        FakeApi.error_at, FakeApi.error_value = None, None

    def args(self, *options, name="run"):
        return d.parser().parse_args(["run", "--date-from", "2026-10-01", "--date-to", "2026-10-02",
                                      "--output", str(self.base / name), *options])

    def invoke(self, *options, name="run", bridge=None):
        return d.run(self.args(*options, name=name), client_factory=FakeApi, collector=bridge or collector)

    def input_file(self, rows, name="input.json"):
        path = self.base / name
        d.write_json(path, {"videos": rows})
        return str(path)

    def config_file(self, values):
        path = self.base / "task.json"
        d.write_json(path, values)
        return str(path)

    def load_output(self, name="run", filename="videos.json"):
        return json.loads((self.base / name / filename).read_text(encoding="utf-8-sig"))

    def rules(self, *options):
        return d.prepare(self.args("--keyword", "Python", *options))


class RuleTests(BaseTests):
    def test_beijing_natural_day_inclusive_end(self):
        rules = self.rules()
        self.assertEqual(rules["begin_timestamp"], int(datetime(2026, 9, 30, 16, tzinfo=timezone.utc).timestamp()))
        self.assertEqual(rules["end_timestamp_exclusive"], int(datetime(2026, 10, 2, 16, tzinfo=timezone.utc).timestamp()))
        for offset, reason in ((-1, "date_mismatch"), (0, None), (172799, None), (172800, "date_mismatch")):
            normalized, _ = d.normalize_video(video(pubdate=rules["begin_timestamp"] + offset), ["Python"], BV0, "test")
            self.assertEqual(d.matches(*normalized, rules), reason)

    def test_rolling_window_uses_china_today_not_utc_day(self):
        args = d.parser().parse_args(["run", "--keyword", "Python", "--last-days", "2"])
        rules = d.prepare(args, now=datetime(2026, 10, 23, 17, tzinfo=timezone.utc))
        self.assertEqual((rules["date_from"], rules["date_to"]), ("2026-10-23", "2026-10-24"))

    def test_invalid_dates_are_not_invented(self):
        for start, end in (("2026-2-01", "2026-10-02"), ("2026-02-30", "2026-10-02"),
                           ("2026-10-03", "2026-10-02"), ("1969-01-01", "2026-10-02")):
            with self.subTest(start=start), self.assertRaises(d.DiscoverError):
                self.rules("--date-from", start, "--date-to", end)
        with self.assertRaises(d.DiscoverError):
            d.prepare(d.parser().parse_args(["run", "--keyword", "x", "--date-from", "2026-10-01"]))
        with self.assertRaises(d.DiscoverError):
            self.rules("--last-days", "7")

    def test_cli_date_mode_overrides_config_mode(self):
        config = self.config_file({"lookback_days": 5, "keywords": ["Python"]})
        rules = d.prepare(self.args("--config", config))
        self.assertEqual(rules["date_from"], "2026-10-01")
        config = self.config_file({"date_from": "2025-01-01", "date_to": "2025-01-02", "keywords": ["Python"]})
        args = d.parser().parse_args(["run", "--config", config, "--last-days", "1"])
        rules = d.prepare(args, now=datetime(2026, 10, 24, tzinfo=d.CHINA))
        self.assertEqual(rules["date_from"], "2026-10-24")

    def test_unknown_or_side_effect_config_fields_rejected(self):
        for key in ("download", "cookies_file", "source_key", "exec", "output", "url"):
            with self.subTest(key=key), self.assertRaises(d.DiscoverError) as error:
                d.prepare(self.args("--config", self.config_file({key: "synthetic-secret"}), "--keyword", "Python"))
            self.assertEqual(error.exception.code, "invalid_config")
            self.assertNotIn("synthetic-secret", error.exception.message)

    def test_option_budgets_are_bounded_and_typed(self):
        for options in (("--limit", "0"), ("--limit", "101"), ("--max-pages", "51"), ("--max-candidates", "1001"),
                        ("--max-requests", "501"), ("--interval", "0"), ("--interval", "nan"),
                        ("--timeout", "121"), ("--max-size-mib", "0")):
            with self.subTest(options=options), self.assertRaises(d.DiscoverError):
                self.rules(*options)
        with self.assertRaises(d.DiscoverError):
            d.prepare(self.args("--config", self.config_file({"limit": True, "keywords": ["Python"]})))

    def test_partition_versions_are_not_mixed(self):
        with self.assertRaises(d.DiscoverError) as error:
            self.rules("--tid", "201", "--tid-v2", "10001")
        self.assertEqual(error.exception.code, "ambiguous_partition")
        with self.assertRaises(d.DiscoverError) as error:
            d.prepare(self.args("--tid-v2", "10001"))
        self.assertEqual(error.exception.code, "missing_seed")
        self.assertTrue(d.prepare(self.args("--tid", "201"))["tids"])

    def test_job_expansion_respects_modes_and_limit(self):
        rules = self.rules("--keyword", "Go", "--keyword-match", "any", "--tid", "201", "--tid", "231")
        jobs = d.jobs_for(rules)
        self.assertEqual(len(jobs), 4)
        self.assertEqual({j["query"] for j in jobs}, {"Python", "Go"})
        self.assertEqual(d.jobs_for(d.prepare(self.args("--tid", "201")))[0]["mode"], "region")
        huge = self.rules()
        huge["keywords"], huge["keyword_match"], huge["tids"] = [str(n) for n in range(10)], "any", list(range(1, 11))
        with self.assertRaises(d.DiscoverError):
            d.jobs_for(huge)

    def test_exact_tags_not_title_keywords_and_all_any(self):
        rules = self.rules("--tag", "教程", "--tag", "Python")
        record, _ = d.normalize_video(video(title="Python 教程", tags=[]), ["Python"], BV0, "test")
        self.assertEqual(d.matches(*record, rules), "tag_mismatch")
        rules["tag_match"] = "any"
        self.assertIsNone(d.matches(*record, rules))
        record, _ = d.normalize_video(video(), ["Python教程"], BV0, "test")
        self.assertEqual(d.matches(*record, rules), "tag_mismatch")

    def test_keyword_is_literal_unicode_normalized_and_field_scoped(self):
        rules = self.rules()
        rules["keywords"] = ["C++ (1)"]
        record, _ = d.normalize_video(video(title="Ｃ＋＋ (1) 实例"), ["x"], BV0, "test")
        self.assertIsNone(d.matches(*record, rules))
        rules["keywords"], rules["keyword_fields"] = ["合成数据"], ["title"]
        self.assertEqual(d.matches(*record, rules), "keyword_mismatch")
        rules["keyword_fields"] = ["description"]
        self.assertIsNone(d.matches(*record, rules))
        rules["keywords"], rules["keyword_match"] = ["missing", "合成数据"], "any"
        self.assertIsNone(d.matches(*record, rules))

    def test_partition_exact_missing_and_name_match(self):
        record, _ = d.normalize_video(video(), [], BV0, "test")
        rules = self.rules("--tid", "36")
        self.assertEqual(d.matches(*record, rules), "partition_mismatch")  # No guessed parent hierarchy.
        rules["tids"], rules["tid_v2s"] = [], [10001]
        self.assertIsNone(d.matches(*record, rules))
        record[0]["tid_v2"] = None
        self.assertEqual(d.matches(*record, rules), "unverified_partition")
        rules["tid_v2s"], rules["partition_names"] = [], ["合成分区"]
        self.assertIsNone(d.matches(*record, rules))

    def test_unknown_fields_not_fabricated_as_zero_or_tags(self):
        normalized, reason = d.normalize_video(video(stat={"view": -1}, tags=None, owner={}), None, BV0, "user_import")
        record, _ = normalized
        self.assertIsNone(reason)
        self.assertIsNone(record["tags"])
        self.assertIsNone(record["author"])
        self.assertTrue(all(v is None for v in record["stats"].values()))
        self.assertEqual(d.matches(*normalized, self.rules("--tag", "教程")), "unverified_tags")

    def test_bgm_is_not_video_tag_and_identity_is_checked(self):
        normalized, _ = d.normalize_video(video(), [{"tag_type": "bgm", "tag_name": "教程"}, {"tag_name": "Python"}], BV0, "test")
        self.assertEqual(normalized[0]["tags"], ["Python"])
        with self.assertRaises(d.DiscoverError):
            d.normalize_video(video(BV1), [], BV0, "test")
        for changes, expected in (({"pubdate": "2026-10-01"}, "unverified_metadata"),
                                  ({"rights": {"ugc_pay": 1}}, "restricted_video")):
            self.assertEqual(d.normalize_video(video(**changes), [], BV0, "test")[1], expected)


class WorkflowTests(BaseTests):
    def test_dry_run_has_no_network_files_cookie_or_state_reads(self):
        args = self.args("--keyword", "Python", "--dry-run", "--state", str(self.base / "missing-state"),
                         "--cookies-file", str(self.base / "missing-cookie"), "--input", str(self.base / "missing-input"))
        with patch.object(d, "load_collector", side_effect=AssertionError("no bridge")), \
             patch.object(d, "state_session", side_effect=AssertionError("no state")):
            result = d.run(args, client_factory=lambda *_a, **_k: self.fail("no network"))
        self.assertTrue(result["ok"])
        self.assertFalse(result["network"])
        self.assertFalse((self.base / "run").exists())

    def test_import_filters_all_dimensions_and_exports_push_compatible_items(self):
        filename = self.input_file([video(), video(BV1, tags=["Python"]), video(BV2, tid=999)])
        result = self.invoke("--input", filename, "--keyword", "Python", "--tag", "教程", "--tid", "201")
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["matched"], 1)
        self.assertEqual(result["api_requests"], 0)
        self.assertFalse(FakeApi.instances)
        records = self.load_output()["videos"]
        self.assertEqual(records[0]["verification_source"], "user_import")
        self.assertEqual(records[0]["part_count"], 2)
        self.assertEqual(records[0]["external_id"], f"bilibili:{BV0}:p1")
        items = push.load_items(self.base / "run/items.json")
        self.assertEqual(items[0]["external_id"], records[0]["external_id"])
        self.assertIn("教程", items[0]["tags"])
        self.assertEqual(push.normalize(items[0]), items[0])
        for output in (self.base / "run").glob("*.json"):
            text = output.read_text(encoding="utf-8")
            self.assertNotIn("synthetic-secret", text)
            self.assertNotIn("不应复制的简介", text)
            self.assertNotIn("evil.invalid", text)

    def test_exported_videos_json_can_be_reimported_without_losing_stats(self):
        filename = self.input_file([video()])
        self.invoke("--input", filename)
        first = self.load_output()["videos"][0]
        result = self.invoke("--input", str(self.base / "run/videos.json"), name="reimport")
        self.assertTrue(result["ok"])
        second = self.load_output("reimport")["videos"][0]
        for key in ("bvid", "author", "author_id", "pubdate", "tags", "stats", "total_duration_seconds", "part_count"):
            self.assertEqual(second[key], first[key], key)
        self.assertEqual(second["verification_source"], "user_import")

    def test_online_details_and_real_tags_overrule_search_fields(self):
        FakeApi.pages[("Python", 0, 1)] = {"result": [{"bvid": BV0, "pubdate": 0}, {"bvid": BV1, "tag": "教程"}], "numPages": 1}
        FakeApi.tag_values[BV1] = ["Python"]
        result = self.invoke("--keyword", "Python", "--tag", "教程")
        self.assertTrue(result["ok"])
        self.assertEqual(result["matched"], 1)
        self.assertEqual(result["rejections"]["tag_mismatch"], 1)
        self.assertEqual(result["api_requests"], 6)  # Bootstrap + search + 2*(details+tags).
        self.assertEqual(self.load_output()["videos"][0]["verification_source"], "bilibili_detail_and_tags")

    def test_candidate_duplicates_do_not_trigger_duplicate_detail_calls(self):
        FakeApi.pages[("Python", 0, 1)] = {"result": [{"bvid": BV0}, {"bvid": BV0}], "numPages": 1}
        result = self.invoke("--keyword", "Python")
        self.assertEqual(result["matched"], 1)
        self.assertEqual(result["api_requests"], 4)
        self.assertEqual(self.load_output(filename="candidates.json")["candidates"][1]["decision"], "duplicate")

    def test_global_page_budget_and_round_robin_queries(self):
        FakeApi.pages[("Python", 0, 1)] = {"result": [{"bvid": BV0}], "numPages": 10}
        FakeApi.pages[("Go", 0, 1)] = {"result": [{"bvid": BV1}], "numPages": 10}
        result = self.invoke("--keyword", "Python", "--keyword", "Go", "--keyword-match", "any", "--max-pages", "2")
        self.assertEqual(result["pages"], 2)
        self.assertTrue(result["truncated"])
        self.assertEqual(result["stop_reason"], "max_pages")
        calls = [p for path, p in FakeApi.instances[0].calls if path.endswith("search/type")]
        self.assertEqual([(p["keyword"], p["page"]) for p in calls], [("Python", 1), ("Go", 1)])

    def test_region_only_uses_old_tid_and_post_filters(self):
        FakeApi.pages[(201, 1)] = {"archives": [{"bvid": BV0}], "page": {"count": 1, "size": 20}}
        result = self.invoke("--tid", "201")
        self.assertTrue(result["ok"])
        self.assertEqual(result["matched"], 1)
        self.assertTrue(FakeApi.instances[0].calls[0][0].endswith("newlist"))

    def test_limit_and_candidate_budget_are_honest_truncations(self):
        filename = self.input_file([video(), video(BV1), video(BV2)])
        result = self.invoke("--input", filename, "--limit", "1")
        self.assertTrue(result["ok"])
        self.assertEqual((result["matched"], result["stop_reason"]), (1, "limit"))
        result = self.invoke("--input", filename, "--max-candidates", "1", name="candidate-cap")
        self.assertEqual((result["examined"], result["stop_reason"]), (1, "max_candidates"))
        self.assertEqual(result["coverage"], "bounded_not_exhaustive")

    def test_request_budget_during_verification_does_not_include_unverified_video(self):
        FakeApi.pages[("Python", 0, 1)] = {"result": [{"bvid": BV0}], "numPages": 1}
        result = self.invoke("--keyword", "Python", "--max-requests", "3")
        self.assertTrue(result["ok"])
        self.assertEqual(result["api_requests"], 3)
        self.assertEqual(result["matched"], 0)
        self.assertEqual(result["stop_reason"], "max_requests")
        self.assertEqual(self.load_output(filename="candidates.json")["candidates"][0]["decision"], "unverified")

    def test_failure_keeps_partial_data_but_not_state(self):
        FakeApi.pages[("Python", 0, 1)] = {"result": [{"bvid": BV0}, {"bvid": BV1}], "numPages": 1}
        FakeApi.error_at, FakeApi.error_value = 5, RuntimeError("synthetic-secret must not be echoed")
        state = self.base / "seen.json"
        result = self.invoke("--keyword", "Python", "--state", str(state))
        self.assertFalse(result["ok"])
        self.assertEqual(result["status"], "partial")
        self.assertEqual(result["matched"], 1)
        self.assertNotIn("synthetic-secret", json.dumps(result))
        self.assertFalse(state.exists())
        self.assertFalse(state.with_name("seen.json.lock").exists())
        self.assertEqual(len(self.load_output()["videos"]), 1)

    def test_interruption_preserves_manifest_without_committing_state(self):
        FakeApi.pages[("Python", 0, 1)] = {"result": [{"bvid": BV0}], "numPages": 1}
        FakeApi.error_at, FakeApi.error_value = 3, KeyboardInterrupt()
        result = self.invoke("--keyword", "Python", "--state", str(self.base / "seen.json"))
        self.assertEqual(result["status"], "interrupted")
        self.assertFalse(result["state_committed"])
        self.assertTrue((self.base / "run/manifest.json").is_file())

    def test_unknown_candidate_not_followed_as_arbitrary_url(self):
        FakeApi.pages[("Python", 0, 1)] = {"result": [{"bvid": "https://evil.invalid/steal"}, {"url": "https://evil.invalid/"}], "numPages": 1}
        result = self.invoke("--keyword", "Python")
        self.assertTrue(result["ok"])
        self.assertEqual(result["matched"], 0)
        self.assertEqual(result["api_requests"], 2)
        self.assertEqual(result["rejections"]["unverified_candidate"], 2)

    def test_output_directory_never_overwritten(self):
        folder = self.base / "run"
        folder.mkdir()
        (folder / "manifest.json").write_text("sentinel", encoding="utf-8")
        with self.assertRaises(d.DiscoverError) as error:
            self.invoke("--keyword", "Python")
        self.assertEqual(error.exception.code, "output_exists")
        self.assertEqual((folder / "manifest.json").read_text(), "sentinel")

    def test_csv_formula_defense_does_not_change_json_title(self):
        filename = self.input_file([video(title="  =HYPERLINK(\"https://evil.invalid\")")])
        self.invoke("--input", filename)
        self.assertTrue(self.load_output()["videos"][0]["title"].startswith("=HYPERLINK"))
        raw = (self.base / "run/videos.csv").read_bytes()
        self.assertTrue(raw.startswith(b"\xef\xbb\xbf"))
        self.assertIn("'=HYPERLINK", raw.decode("utf-8-sig"))
        for value in ("=1+1", "+cmd", "-cmd", "@SUM(1)", " \t=1", "\tvalue"):
            self.assertTrue(d.spreadsheet_text(value).startswith("'"))

    def test_media_requires_explicit_cli_and_forwards_only_matches(self):
        filename = self.input_file([video(), video(BV1, tags=["Python"])])
        calls = []
        def media(args):
            calls.append(args)
            return {"ok": True, "downloaded": 1, "link_items": 1}
        bridge = SimpleNamespace(metadata=collector.metadata, parser=collector.parser, collect=media)
        result = self.invoke("--input", filename, "--tag", "教程", bridge=bridge)
        self.assertTrue(result["ok"])
        self.assertFalse(calls)
        result = self.invoke("--input", filename, "--tag", "教程", "--download", name="media", bridge=bridge)
        self.assertTrue(result["ok"])
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0].urls, [f"https://www.bilibili.com/video/{BV0}/?p=1"])
        self.assertTrue(calls[0].download)
        self.assertFalse(calls[0].subtitles)
        self.assertEqual(result["media"]["downloaded"], 1)

    def test_media_failure_keeps_state_uncommitted_and_retains_data(self):
        filename = self.input_file([video()])
        bridge = SimpleNamespace(metadata=collector.metadata, parser=collector.parser,
                                 collect=lambda _a: {"ok": False, "downloaded": 0, "link_items": 1})
        state = self.base / "seen.json"
        result = self.invoke("--input", filename, "--download", "--state", str(state), bridge=bridge)
        self.assertFalse(result["ok"])
        self.assertEqual(result["error"], "media_failed")
        self.assertFalse(state.exists())
        self.assertEqual(len(self.load_output()["videos"]), 1)

    def test_subtitles_only_forward_is_not_a_download(self):
        filename = self.input_file([video()])
        calls = []
        def media(args):
            calls.append(args)
            return {"ok": True, "downloaded": 0, "link_items": 1}
        bridge = SimpleNamespace(metadata=collector.metadata, parser=collector.parser, collect=media)
        result = self.invoke("--input", filename, "--subtitles", bridge=bridge)
        self.assertTrue(result["ok"])
        self.assertTrue(calls[0].subtitles)
        self.assertFalse(calls[0].download)
        self.assertEqual(result["media"]["downloaded"], 0)

    def test_real_cli_import_and_existing_push_dry_run(self):
        filename = self.input_file([video()])
        command = [sys.executable, str(SKILL / "scripts/bilibili_discover.py"), "run", "--input", filename,
                   "--date-from", "2026-10-01", "--date-to", "2026-10-02", "--tag", "教程",
                   "--output", str(self.base / "cli"), "--state", str(self.base / "cli-state.json")]
        complete = subprocess.run(command, capture_output=True, text=True, encoding="utf-8", timeout=30)
        self.assertEqual(complete.returncode, 0, complete.stdout + complete.stderr)
        self.assertEqual(json.loads(complete.stdout)["api_requests"], 0)
        preflight = subprocess.run([sys.executable, str(ROOT / "skills/wzstudy-push/scripts/wzstudy_push.py"),
                                    "push", "--input", str(self.base / "cli/items.json"), "--dry-run"],
                                   capture_output=True, text=True, encoding="utf-8", timeout=30)
        self.assertEqual(preflight.returncode, 0, preflight.stdout + preflight.stderr)
        self.assertEqual(json.loads(preflight.stdout)["network_requests"], 0)


class StateTests(BaseTests):
    def test_cross_run_dedup_and_explicit_refresh(self):
        filename = self.input_file([video(), video(BV1)])
        state = self.base / "seen.json"
        first = self.invoke("--input", filename, "--state", str(state))
        self.assertTrue(first["state_committed"])
        second = self.invoke("--input", filename, "--state", str(state), name="second")
        self.assertEqual((second["matched"], second["skipped_seen"]), (0, 2))
        d.write_json(Path(filename), {"videos": [video(stat={"view": 999})]})
        third = self.invoke("--input", filename, "--state", str(state), "--refresh", name="third")
        self.assertEqual(third["matched"], 1)
        self.assertEqual(self.load_output("third")["videos"][0]["stats"]["view"], 999)
        self.assertEqual(len(json.loads(state.read_text())["seen"]), 2)

    def test_state_is_global_across_filter_changes(self):
        filename = self.input_file([video()])
        state = self.base / "seen.json"
        self.invoke("--input", filename, "--keyword", "Python", "--state", str(state))
        second = self.invoke("--input", filename, "--tag", "教程", "--state", str(state), name="second")
        self.assertEqual(second["skipped_seen"], 1)

    def test_state_lock_is_not_removed_or_overridden(self):
        state = self.base / "seen.json"
        lock = self.base / "seen.json.lock"
        lock.write_text("another-owner", encoding="ascii")
        with self.assertRaises(d.DiscoverError) as error:
            self.invoke("--keyword", "Python", "--state", str(state))
        self.assertEqual(error.exception.code, "state_locked")
        self.assertEqual(lock.read_text(), "another-owner")
        self.assertFalse((self.base / "run").exists())

    def test_invalid_state_and_unsafe_state_path_rejected(self):
        state = self.base / "seen.json"
        state.write_text('{"synthetic-secret": "keep"}', encoding="utf-8")
        with self.assertRaises(d.DiscoverError):
            self.invoke("--keyword", "Python", "--state", str(state))
        self.assertIn("synthetic-secret", state.read_text())
        self.assertFalse((self.base / "seen.json.lock").exists())
        with self.assertRaises(d.DiscoverError) as error:
            self.invoke("--keyword", "Python", "--state", str(self.base / "run/items.json"))
        self.assertEqual(error.exception.code, "invalid_state_path")
        with self.assertRaises(d.DiscoverError):
            self.rules("--refresh")

    def test_data_only_state_then_media_requires_explicit_refresh(self):
        filename = self.input_file([video()])
        state = self.base / "seen.json"
        self.invoke("--input", filename, "--state", str(state))
        calls = []
        def media(args):
            calls.append(args)
            return {"ok": True, "downloaded": 1, "link_items": 1}
        bridge = SimpleNamespace(metadata=collector.metadata, parser=collector.parser, collect=media)
        skipped = self.invoke("--input", filename, "--state", str(state), "--download", name="skipped-media", bridge=bridge)
        self.assertTrue(skipped["ok"])
        self.assertEqual(skipped["media"]["status"], "not_run")
        self.assertIn("--refresh", skipped["media_note"])
        self.assertFalse(calls)
        refreshed = self.invoke("--input", filename, "--state", str(state), "--refresh", "--download", name="refreshed-media", bridge=bridge)
        self.assertTrue(refreshed["ok"])
        self.assertEqual(len(calls), 1)

    def test_final_receipt_failure_reports_committed_state_truthfully(self):
        filename = self.input_file([video()])
        state = self.base / "seen.json"
        original = d.write_json
        def write(path, value):
            if path.name == "manifest.json" and value.get("state_committed"):
                raise OSError("synthetic-secret receipt failure")
            return original(path, value)
        with patch.object(d, "write_json", write):
            result = self.invoke("--input", filename, "--state", str(state))
        self.assertFalse(result["ok"])
        self.assertEqual(result["error"], "receipt_write_failed")
        self.assertTrue(result["state_committed"])
        self.assertIn(BV0, json.loads(state.read_text())["seen"])
        self.assertEqual(result["output"], str((self.base / "run").resolve()))
        self.assertNotIn("synthetic-secret", json.dumps(result))
        self.assertFalse((self.base / "seen.json.lock").exists())

    def test_state_capacity_refuses_silent_eviction_or_overwrite(self):
        path = self.base / "seen.json"
        d.write_json(path, {"schema_version": 1, "seen": {BV0: "existing"}})
        original = path.read_bytes()
        state = {"schema_version": 1, "seen": {f"BV{n:010d}": "2026-10-01T00:00:00Z" for n in range(50001)}}
        with self.assertRaises(d.DiscoverError) as error:
            d.commit_state(path, state)
        self.assertEqual(error.exception.code, "state_capacity")
        self.assertEqual(path.read_bytes(), original)

    def test_failed_run_does_not_modify_existing_state(self):
        filename = self.input_file([video()])
        state = self.base / "seen.json"
        self.invoke("--input", filename, "--state", str(state))
        original = state.read_bytes()
        FakeApi.error_at, FakeApi.error_value = 1, d.DiscoverError("access_restricted", "blocked")
        failed = self.invoke("--keyword", "Python", "--state", str(state), name="failed")
        self.assertFalse(failed["ok"])
        self.assertEqual(state.read_bytes(), original)


class Response:
    def __init__(self, value=None, status=200, raw=None, cookies=()):
        self.status = status
        self.raw = raw if raw is not None else json.dumps(value or {"code": 0, "data": {}}, ensure_ascii=False).encode("utf-8")
        self.headers = Message()
        for cookie in cookies:
            self.headers.add_header("Set-Cookie", cookie)
        self.read_calls = 0

    def read(self, count):
        self.read_calls += 1
        return self.raw[:count]

    def info(self):
        return self.headers


class Transport:
    def __init__(self, *responses):
        self.responses, self.calls, self.closed = list(responses), [], 0

    def __call__(self, host, timeout):
        parent = self
        class Connection:
            def request(self, method, path, headers):
                parent.calls.append((host, method, path, headers))
            def getresponse(self):
                value = parent.responses.pop(0)
                if isinstance(value, Exception):
                    raise value
                return value
            def close(self):
                parent.closed += 1
        return Connection()


class TransportTests(BaseTests):
    def api(self, transport, *options, cookies=None, sleeper=None):
        return d.ApiClient(self.rules(*options), cookies_file=cookies, collector=collector,
                           connection=transport, sleeper=sleeper or (lambda _: None), timer=lambda: 0)

    def test_normal_anonymous_cookie_is_only_accepted_from_server(self):
        home = Response(cookies=("buvid3=provided-by-server; Domain=.bilibili.com; Path=/; Secure",))
        transport = Transport(home, Response())
        client = self.api(transport)
        client.bootstrap()
        client.json("/x/web-interface/view", {"bvid": BV0})
        self.assertEqual(home.read_calls, 0)
        self.assertNotIn("Cookie", transport.calls[0][3])
        self.assertEqual(transport.calls[1][3]["Cookie"], "buvid3=provided-by-server")
        self.assertEqual(client.requests, 2)

    def test_no_fabricated_device_cookie_when_server_sets_none(self):
        transport = Transport(Response(), Response())
        client = self.api(transport)
        client.bootstrap()
        client.json("/x/web-interface/view", {"bvid": BV0})
        self.assertNotIn("Cookie", transport.calls[1][3])
        self.assertEqual(len(client.jar), 0)

    def test_explicit_cookie_read_only_and_off_domain_filtered(self):
        path = self.base / "cookies.txt"
        original = ("# Netscape HTTP Cookie File\n"
                    ".bilibili.com\tTRUE\t/\tTRUE\t2147483647\tbuvid3\tsynthetic-bili-secret\n"
                    ".evil.invalid\tTRUE\t/\tTRUE\t2147483647\tother\tsynthetic-other-secret\n")
        path.write_text(original, encoding="utf-8")
        transport = Transport(Response())
        client = self.api(transport, cookies=str(path))
        client.bootstrap()  # A valid user-provided cookie means no bootstrap request.
        self.assertEqual(client.requests, 0)
        client.json("/x/web-interface/view", {"bvid": BV0})
        self.assertIn("synthetic-bili-secret", transport.calls[0][3]["Cookie"])
        self.assertNotIn("synthetic-other-secret", transport.calls[0][3]["Cookie"])
        self.assertEqual(path.read_text(encoding="utf-8"), original)

    def test_invalid_cookie_parser_does_not_echo_raw_lines(self):
        path = self.base / "bad-cookie.txt"
        path.write_text("# Netscape HTTP Cookie File\n.bilibili.com\tTRUE\t/\tTRUE\tNOT-AN-EXPIRY\tSESSDATA\tsynthetic-secret\n", encoding="utf-8")
        output, errors = io.StringIO(), io.StringIO()
        with redirect_stdout(output), redirect_stderr(errors), self.assertRaises(d.DiscoverError) as error:
            self.api(Transport(), cookies=str(path))
        self.assertNotIn("synthetic-secret", output.getvalue() + errors.getvalue() + error.exception.message)

    def test_api_path_is_fixed_and_query_is_encoded(self):
        transport = Transport(Response())
        client = self.api(transport)
        with self.assertRaises(d.DiscoverError):
            client.json("https://evil.invalid/", {})
        self.assertEqual(client.requests, 0)
        client.json("/x/web-interface/search/type", {"keyword": "x&evil=1\r\nCookie: bad"})
        self.assertIn("%26evil%3D1", transport.calls[0][2])
        self.assertNotIn("\r", transport.calls[0][2])

    def test_restricted_statuses_stop_without_retry(self):
        for status in (401, 403, 412, 429):
            with self.subTest(status=status):
                transport = Transport(Response(status=status, raw=b"synthetic-secret"))
                client = self.api(transport)
                with self.assertRaises(d.DiscoverError) as error:
                    client.json("/x/web-interface/view", {"bvid": BV0})
                self.assertEqual(error.exception.code, "access_restricted")
                self.assertNotIn("synthetic-secret", error.exception.message)
                self.assertEqual((client.requests, len(transport.calls), transport.closed), (1, 1, 1))

    def test_redirects_not_followed_or_cookie_forwarded(self):
        transport = Transport(Response(status=302))
        client = self.api(transport)
        with self.assertRaises(d.DiscoverError) as error:
            client.json("/x/web-interface/view", {"bvid": BV0})
        self.assertEqual(error.exception.code, "redirect_refused")
        self.assertEqual(len(transport.calls), 1)

    def test_signing_risk_and_non_json_responses_stop_safely(self):
        for response in (Response({"code": -352, "message": "synthetic-secret"}),
                         Response({"code": 0, "data": {"v_voucher": "synthetic-secret"}}),
                         Response(raw=b"<html>synthetic-secret captcha</html>"),
                         Response({"code": True, "data": {}})):
            with self.subTest(raw=response.raw[:40]):
                client = self.api(Transport(response))
                with self.assertRaises(d.DiscoverError) as error:
                    client.json("/x/web-interface/view", {"bvid": BV0})
                self.assertNotIn("synthetic-secret", error.exception.message)
                self.assertEqual(client.requests, 1)

    def test_response_size_and_network_errors_are_redacted(self):
        with patch.object(d, "MAX_BODY", 10):
            client = self.api(Transport(Response(raw=b"x" * 11)))
            with self.assertRaises(d.DiscoverError) as error:
                client.json("/x/web-interface/view", {})
            self.assertEqual(error.exception.code, "response_too_large")
        client = self.api(Transport(OSError("synthetic-secret")))
        with self.assertRaises(d.DiscoverError) as error:
            client.json("/x/web-interface/view", {})
        self.assertEqual(error.exception.code, "network_error")
        self.assertNotIn("synthetic-secret", error.exception.message)

    def test_request_budget_and_interval_are_global(self):
        sleeps = []
        transport = Transport(Response(), Response())
        client = self.api(transport, "--max-requests", "2", sleeper=sleeps.append)
        client.json("/x/web-interface/view", {})
        client.json("/x/tag/archive/tags", {})
        with self.assertRaises(d.BudgetReached):
            client.json("/x/web-interface/search/type", {})
        self.assertEqual(client.requests, 2)
        self.assertEqual(sleeps, [2.0])


if __name__ == "__main__":
    unittest.main()
