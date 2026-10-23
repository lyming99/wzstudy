"""Reliable SSE consumer: Python 3 standard library, SQLite data+cursor transactions."""
import json
import os
from pathlib import Path
import sqlite3
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    key = os.environ.get('READ_API_KEY', '')
    if not key:
        raise SystemExit('Set READ_API_KEY in this shell first (never pass it in a URL).')
    base = os.environ.get('WZSTUDY_BASE_URL', 'http://127.0.0.1:8091').rstrip('/')
    sources = ','.join(sorted(set(filter(None, os.environ.get('WZSTUDY_SOURCE_IDS', '').split(',')))))
    cache = Path(os.environ.get('WZSTUDY_CACHE', '.tmp/subscriber.sqlite3'))
    cache.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(cache)
    db.execute('PRAGMA journal_mode=WAL')
    db.execute('CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)')
    db.execute('CREATE TABLE IF NOT EXISTS items(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,body TEXT NOT NULL)')
    existing = db.execute("SELECT value FROM metadata WHERE key='sources'").fetchone()
    if existing and existing[0] != sources:
        raise SystemExit('Subscription changed. Choose a NEW WZSTUDY_CACHE to replay from cursor 0.')
    with db:
        db.execute("INSERT OR IGNORE INTO metadata VALUES('sources',?)", (sources,))
        db.execute("INSERT OR IGNORE INTO metadata VALUES('cursor','0')")
    path = '/api/v1/stream?' + urllib.parse.urlencode({'source_id': sources})
    delay = 1
    try:
        while True:
            cursor = int(db.execute("SELECT value FROM metadata WHERE key='cursor'").fetchone()[0])
            request = urllib.request.Request(base + path, headers={
                'Authorization': 'Bearer ' + key,
                'Accept': 'text/event-stream',
                'Last-Event-ID': str(cursor),
            })
            try:
                with urllib.request.urlopen(request, timeout=45) as response:
                    if not response.headers.get('Content-Type', '').startswith('text/event-stream'):
                        raise RuntimeError('Server did not return an SSE stream')
                    delay = 1
                    for event in events(response):
                        if event['event'] == 'error':
                            break
                        if event['event'] not in ('change', 'checkpoint'):
                            continue
                        new_cursor = int(event['id'])
                        if new_cursor <= cursor:
                            continue
                        payload = json.loads(event['data'])
                        with db:
                            if event['event'] == 'change':
                                if payload['cursor'] != new_cursor:
                                    raise ValueError('Event cursor mismatch')
                                if payload['operation'] == 'upsert':
                                    item = payload['item']
                                    db.execute('INSERT INTO items VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,body=excluded.body WHERE excluded.revision>=items.revision',
                                               (item['id'], item['revision'], json.dumps(item, ensure_ascii=False)))
                                elif payload['operation'] == 'delete':
                                    db.execute('DELETE FROM items WHERE id=? AND revision<=?', (payload['item_id'], payload['revision']))
                                else:
                                    raise ValueError('Unknown change operation')
                            db.execute("UPDATE metadata SET value=? WHERE key='cursor'", (str(new_cursor),))
                        cursor = new_cursor
                        print(f'cursor={cursor} event={event["event"]}', flush=True)
            except urllib.error.HTTPError as exc:
                if exc.code in (400, 401, 403, 404):
                    raise SystemExit(f'HTTP {exc.code}: verify endpoint, credential and cursor; do not silently reset a cache.') from None
                print(f'HTTP {exc.code}; reconnecting', file=sys.stderr)
            except (urllib.error.URLError, OSError, TimeoutError):
                print('Connection interrupted; resuming from the last committed cursor', file=sys.stderr)
            time.sleep(delay)
            delay = min(delay * 2, 30)
    except KeyboardInterrupt:
        print('Stopped; cache and cursor retained.')
    finally:
        db.close()


def events(response):
    event = {'event': 'message', 'id': '', 'data': ''}
    for raw in response:
        line = raw.decode('utf-8').rstrip('\r\n')
        if not line:
            if event['data']:
                yield event
            event = {'event': 'message', 'id': '', 'data': ''}
        elif line.startswith(':'):
            continue
        elif ':' in line:
            field, value = line.split(':', 1)
            value = value.removeprefix(' ')
            if field == 'data':
                event['data'] += ('\n' if event['data'] else '') + value
            elif field in ('event', 'id'):
                event[field] = value


if __name__ == '__main__':
    main()
