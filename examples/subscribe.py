"""Client-initiated HTTP polling with atomic SQLite material/cursor updates.

The historical filename is retained for existing callers. Recommendation and
learning algorithms belong in the client, using this published-material cache.
Only Python's standard library is required.
"""
import json
import os
from pathlib import Path
import sqlite3
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def open_cache(path, sources, base):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(path)
    db.execute('PRAGMA journal_mode=WAL')
    db.execute('CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)')
    db.execute('CREATE TABLE IF NOT EXISTS items(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,body TEXT NOT NULL)')
    for name, value in [('sources', sources), ('base_url', base)]:
        existing = db.execute('SELECT value FROM metadata WHERE key=?', (name,)).fetchone()
        if existing and existing[0] != value:
            db.close()
            raise ValueError('Server or source selection changed. Choose a NEW WZSTUDY_CACHE to replay from cursor 0.')
    with db:
        db.execute("INSERT OR IGNORE INTO metadata VALUES('sources',?)", (sources,))
        db.execute("INSERT OR IGNORE INTO metadata VALUES('base_url',?)", (base,))
        db.execute("INSERT OR IGNORE INTO metadata VALUES('cursor','0')")
    return db


def apply_page(db, page, cursor):
    """Apply one bounded changes window and its cursor, including empty pages."""
    next_cursor = page['next_cursor']
    high_watermark = page['high_watermark']
    changes = page['changes']
    has_more = page['has_more']
    if (type(next_cursor) is not int or type(high_watermark) is not int
            or not cursor <= next_cursor <= high_watermark
            or not isinstance(changes, list) or type(has_more) is not bool
            or has_more != (next_cursor < high_watermark)
            or (has_more and next_cursor == cursor)):
        raise ValueError('Invalid change window; keep the last committed cursor.')
    previous = cursor
    with db:
        for change in changes:
            event_cursor = change['cursor']
            if type(event_cursor) is not int or not previous < event_cursor <= next_cursor:
                raise ValueError('Unordered or out-of-window change cursor.')
            previous = event_cursor
            if change['operation'] == 'upsert':
                item = change['item']
                if item['id'] != change['item_id'] or item['revision'] != change['revision']:
                    raise ValueError('Change snapshot mismatch.')
                db.execute('INSERT INTO items VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,body=excluded.body WHERE excluded.revision>=items.revision',
                           (item['id'], item['revision'], json.dumps(item, ensure_ascii=False)))
            elif change['operation'] == 'delete':
                db.execute('DELETE FROM items WHERE id=? AND revision<=?', (change['item_id'], change['revision']))
            else:
                raise ValueError('Unknown change operation.')
        db.execute("UPDATE metadata SET value=? WHERE key='cursor'", (str(next_cursor),))
    return next_cursor


def main():
    key = os.environ.get('WZSTUDY_CLIENT_KEY') or os.environ.get('READ_API_KEY', '')
    if not key:
        raise SystemExit('Set WZSTUDY_CLIENT_KEY (sync permission), or legacy READ_API_KEY. Never pass a key in a URL.')
    base = os.environ.get('WZSTUDY_BASE_URL', 'http://127.0.0.1:8091').rstrip('/')
    sources = ','.join(sorted(set(filter(None, os.environ.get('WZSTUDY_SOURCE_IDS', '').split(',')))))
    interval = float(os.environ.get('WZSTUDY_POLL_SECONDS', '30'))
    if not 1 <= interval <= 3600:
        raise SystemExit('WZSTUDY_POLL_SECONDS must be 1-3600.')
    db = open_cache(os.environ.get('WZSTUDY_CACHE', '.tmp/subscriber.sqlite3'), sources, base)
    delay = 1
    try:
        while True:
            cursor = int(db.execute("SELECT value FROM metadata WHERE key='cursor'").fetchone()[0])
            query = urllib.parse.urlencode({'after': cursor, 'limit': 100, 'source_id': sources})
            request = urllib.request.Request(base + '/api/v1/changes?' + query, headers={
                'Authorization': 'Bearer ' + key,
                'Accept': 'application/json',
            })
            try:
                with urllib.request.urlopen(request, timeout=35) as response:
                    page = json.load(response)['data']
                cursor = apply_page(db, page, cursor)
                print(f'cursor={cursor} changes={len(page["changes"])}', flush=True)
                delay = 1
                if not page['has_more']:
                    time.sleep(interval)
            except urllib.error.HTTPError as exc:
                if exc.code in (400, 401, 403, 404):
                    raise SystemExit(f'HTTP {exc.code}: verify endpoint, credential and cursor; do not silently reset a cache.') from None
                print(f'HTTP {exc.code}; retrying a read-only query', file=sys.stderr)
                time.sleep(delay)
                delay = min(delay * 2, 30)
            except (urllib.error.URLError, OSError, TimeoutError):
                print('Query interrupted; retaining the last committed cursor', file=sys.stderr)
                time.sleep(delay)
                delay = min(delay * 2, 30)
    except KeyboardInterrupt:
        print('Stopped; cache and cursor retained.')
    finally:
        db.close()


if __name__ == '__main__':
    main()
