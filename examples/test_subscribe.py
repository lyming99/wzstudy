"""Offline tests for the pull-only SQLite client; never contacts a service."""
import json
from pathlib import Path
import tempfile
import unittest

from subscribe import apply_page, open_cache


class PollingCacheTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.path = Path(self.temp.name) / 'cache.sqlite3'
        self.db = open_cache(self.path, '', 'http://127.0.0.1:8091')
        self.addCleanup(self.temp.cleanup)
        self.addCleanup(self.db.close)

    def cursor(self):
        return int(self.db.execute("SELECT value FROM metadata WHERE key='cursor'").fetchone()[0])

    @staticmethod
    def upsert(cursor=1, revision=1):
        return {'cursor': cursor, 'operation': 'upsert', 'item_id': 'lesson', 'revision': revision,
                'item': {'id': 'lesson', 'revision': revision, 'title': 'published lesson'}}

    def test_upsert_and_delete(self):
        apply_page(self.db, {'changes': [self.upsert()], 'next_cursor': 1, 'high_watermark': 1, 'has_more': False}, 0)
        row = self.db.execute('SELECT body FROM items WHERE id=?', ('lesson',)).fetchone()
        self.assertEqual(json.loads(row[0])['title'], 'published lesson')
        apply_page(self.db, {'changes': [{'cursor': 2, 'operation': 'delete', 'item_id': 'lesson', 'revision': 2}],
                             'next_cursor': 2, 'high_watermark': 2, 'has_more': False}, 1)
        self.assertEqual(self.cursor(), 2)
        self.assertEqual(self.db.execute('SELECT count(*) FROM items').fetchone()[0], 0)

    def test_filtered_empty_page_advances_cursor(self):
        apply_page(self.db, {'changes': [], 'next_cursor': 4, 'high_watermark': 6, 'has_more': True}, 0)
        self.assertEqual(self.cursor(), 4)
        apply_page(self.db, {'changes': [], 'next_cursor': 6, 'high_watermark': 6, 'has_more': False}, 4)
        self.assertEqual(self.cursor(), 6)

    def test_malformed_event_rolls_back_data_and_cursor(self):
        with self.assertRaises(ValueError):
            apply_page(self.db, {'changes': [self.upsert(), {'cursor': 2, 'operation': 'unknown'}],
                                 'next_cursor': 2, 'high_watermark': 2, 'has_more': False}, 0)
        self.assertEqual(self.cursor(), 0)
        self.assertEqual(self.db.execute('SELECT count(*) FROM items').fetchone()[0], 0)

    def test_invalid_windows_preserve_cache(self):
        for next_cursor, watermark, has_more in [(0, 1, True), (2, 1, False), (1, 2, False), (-1, 1, True)]:
            with self.subTest(next_cursor=next_cursor), self.assertRaises(ValueError):
                apply_page(self.db, {'changes': [], 'next_cursor': next_cursor, 'high_watermark': watermark, 'has_more': has_more}, 0)
        self.assertEqual(self.cursor(), 0)

    def test_unordered_events_roll_back(self):
        with self.assertRaises(ValueError):
            apply_page(self.db, {'changes': [self.upsert(2), self.upsert(1)],
                                 'next_cursor': 2, 'high_watermark': 2, 'has_more': False}, 0)
        self.assertEqual(self.cursor(), 0)
        self.assertEqual(self.db.execute('SELECT count(*) FROM items').fetchone()[0], 0)

    def test_source_selection_or_server_change_requires_new_cache(self):
        for sources, base in [('different-source', 'http://127.0.0.1:8091'), ('', 'http://127.0.0.1:8092')]:
            with self.subTest(sources=sources, base=base), self.assertRaises(ValueError):
                open_cache(self.path, sources, base)


if __name__ == '__main__':
    unittest.main()
