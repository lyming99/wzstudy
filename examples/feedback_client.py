"""Client-side feedback example. No server-side recommendation algorithm.
Use a verified QR ConnectionConfig; explicitly enable its feedback permission.
Outbox scope is server origin + client ID. It stores events/notes, never the Key.
"""
from datetime import datetime, timezone
import json
from pathlib import Path
import sqlite3
import urllib.error
import urllib.parse
import urllib.request
import uuid

from client_connection import ConnectionConfig, ConnectionError, _NoRedirect

ACTIONS = ('recommended', 'impression', 'read', 'like', 'record', 'favorite')
EXCLUSIONS = ('recommended', 'seen', 'read', 'liked', 'recorded', 'favorited')


def make_event(item_id, action, *, value=True, item_revision=0, note='', progress=None, occurred_at=None):
    """Create ONCE when the real recommendation/interaction happens, then enqueue.
    Never create a fresh event_id for an HTTP retry. Candidate GETs are not exposure.
    """
    if action not in ACTIONS or type(value) is not bool or not isinstance(note, str):
        raise ConnectionError('Invalid feedback action.')
    try:
        identifier = str(uuid.UUID(item_id))
    except (ValueError, TypeError, AttributeError):
        raise ConnectionError('Invalid item ID.') from None
    if identifier == str(uuid.UUID(int=0)) or type(item_revision) is not int or item_revision < 0:
        raise ConnectionError('Invalid item revision.')
    if not value and action in ('recommended', 'impression'):
        raise ConnectionError('Exposure cannot be undone; use a cooldown query window.')
    if (note or progress is not None) and (action != 'record' or not value):
        raise ConnectionError('Note/progress require a positive record action.')
    if len(note.encode('utf-8')) > 4000 or '\0' in note:
        raise ConnectionError('Learning note exceeds its limit.')
    if progress is not None and (type(progress) not in (int, float) or not 0 <= progress <= 1):
        raise ConnectionError('Progress must be within zero and one.')
    event = {'event_id': str(uuid.uuid4()), 'item_id': identifier, 'action': action, 'value': value,
             'item_revision': item_revision, 'occurred_at': occurred_at or datetime.now(timezone.utc).isoformat()}
    if note:
        event['note'] = note
    if progress is not None:
        event['progress'] = progress
    return event


class FeedbackClient:
    def __init__(self, config: ConnectionConfig, *, allow_insecure_http=False, timeout=15):
        self.config, self.timeout = config, timeout
        if config.base_url.startswith('http:') and not allow_insecure_http:
            raise ConnectionError('HTTP exposes credentials; explicitly opt in for trusted development only.')
        self.opener = urllib.request.build_opener(_NoRedirect())

    def _request(self, path, body=None):
        request = urllib.request.Request(self.config.base_url + '/api/v1' + path,
                                         data=None if body is None else json.dumps(body, ensure_ascii=False, allow_nan=False).encode('utf-8'),
                                         headers={**self.config.headers(), 'Accept': 'application/json', 'Content-Type': 'application/json'},
                                         method='GET' if body is None else 'POST')
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                raw = response.read(4 * 1024 * 1024 + 1)
                if len(raw) > 4 * 1024 * 1024 or response.headers.get_content_type() != 'application/json':
                    raise ConnectionError('Invalid feedback/query response.')
                data = json.loads(raw.decode('utf-8'))['data']
                if not isinstance(data, dict):
                    raise ValueError()
                return data
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            raise ConnectionError('Feedback/query request failed (HTTP ' + str(status) + '); keep unsent events for retry.') from None
        except (ValueError, KeyError, TypeError, UnicodeError, OSError, urllib.error.URLError):
            raise ConnectionError('Feedback/query failed; no queued event was acknowledged.') from None

    def candidates(self, *, exclude=('recommended', 'read'), limit=20, offset=0, exclude_since=None, **filters):
        if any(flag not in EXCLUSIONS for flag in exclude) or len(set(exclude)) != len(exclude):
            raise ConnectionError('Invalid exclusion state.')
        if any(key not in ('q', 'kind', 'category', 'tag', 'source_id') for key in filters):
            raise ConnectionError('Unsupported candidate filter.')
        params = {**filters, 'limit': limit, 'offset': offset, 'include_feedback': 'true'}
        if exclude:
            params['exclude'] = ','.join(exclude)
        if exclude_since:
            params['exclude_since'] = exclude_since
        return self._request('/items?' + urllib.parse.urlencode(params))

    def report(self, events):
        if not 1 <= len(events) <= 100:
            raise ConnectionError('Feedback batches contain one to 100 events.')
        result = self._request('/client/feedback', {'events': events})
        acknowledgements = result.get('events')
        sent = [event['event_id'] for event in events]
        if (not isinstance(acknowledgements, list) or len(acknowledgements) != len(sent)
                or [ack.get('event_id') for ack in acknowledgements] != sent
                or any(type(ack.get('sequence')) is not int or ack['sequence'] < 1 or type(ack.get('duplicate')) is not bool for ack in acknowledgements)):
            raise ConnectionError('Invalid acknowledgement; keep events to retry with unchanged IDs.')
        return result

    def states(self, *, item_ids=(), limit=20, offset=0):
        params = {'limit': limit, 'offset': offset}
        if item_ids:
            params['item_id'] = ','.join(item_ids)
        return self._request('/client/feedback?' + urllib.parse.urlencode(params))

    def events(self, after=0, limit=100):
        return self._request('/client/feedback/events?' + urllib.parse.urlencode({'after': after, 'limit': limit}))


class FeedbackOutbox:
    """Explicit, local, private learning-event storage; contains no credentials.
    The actual App owns access control/encryption for this database and its notes.
    """
    def __init__(self, path, config: ConnectionConfig):
        self.scope = json.dumps([config.base_url, config.client_id], separators=(',', ':'))
        target = Path(path)
        self.db = sqlite3.connect(str(target))
        self.db.execute('CREATE TABLE IF NOT EXISTS feedback_scope(scope TEXT NOT NULL)')
        row = self.db.execute('SELECT scope FROM feedback_scope LIMIT 1').fetchone()
        if row is not None and row[0] != self.scope:
            self.db.close()
            raise ConnectionError('Use a separate outbox for each server origin and client identity.')
        if row is None:
            self.db.execute('INSERT INTO feedback_scope(scope) VALUES(?)', (self.scope,))
        self.db.execute('CREATE TABLE IF NOT EXISTS feedback_outbox(position INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT UNIQUE NOT NULL, payload TEXT NOT NULL)')
        self.db.commit()
        target.chmod(0o600)

    def enqueue(self, event):
        payload = json.dumps(event, ensure_ascii=False, sort_keys=True, allow_nan=False)
        old = self.db.execute('SELECT payload FROM feedback_outbox WHERE event_id=?', (event['event_id'],)).fetchone()
        if old is not None:
            if old[0] != payload:
                raise ConnectionError('Event ID was reused with different input.')
            return
        with self.db:
            self.db.execute('INSERT INTO feedback_outbox(event_id,payload) VALUES(?,?)', (event['event_id'], payload))

    def pending(self):
        return self.db.execute('SELECT count(*) FROM feedback_outbox').fetchone()[0]

    def flush(self, client: FeedbackClient):
        if self.scope != json.dumps([client.config.base_url, client.config.client_id], separators=(',', ':')):
            raise ConnectionError('Outbox and destination identity differ.')
        rows = self.db.execute('SELECT event_id,payload FROM feedback_outbox ORDER BY position LIMIT 100').fetchall()
        if not rows:
            return None
        result = client.report([json.loads(row[1]) for row in rows])
        # Deletion only follows a fully validated, successful batch acknowledgement.
        with self.db:
            self.db.executemany('DELETE FROM feedback_outbox WHERE event_id=?', [(row[0],) for row in rows])
        return result

    def close(self):
        self.db.close()
