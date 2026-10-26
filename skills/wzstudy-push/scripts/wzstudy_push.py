#!/usr/bin/env python3
"""WzStudy source-only ingestion client. Python 3.10+, standard library only.
No browser/admin passwords, no automatic web fetching, redirects or write retries.
"""
from __future__ import annotations

import argparse
from datetime import datetime
import hashlib
import http.client
import ipaddress
import json
import mimetypes
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import sys
import tempfile
from urllib.parse import urlsplit, urlunsplit
from uuid import UUID

MAX_JSON = 4 * 1024 * 1024
MAX_UPLOAD = 200 * 1024 * 1024
STRING_LIMITS = {'external_id': 512, 'title': 500, 'summary': 8000, 'content': 262144,
                 'author': 200, 'category': 200, 'language': 35, 'url': 2048}
FIELDS = set(STRING_LIMITS) | {'format', 'kind', 'tags', 'published_at', 'source_updated_at', 'assets'}
MIME = {'.md':'text/markdown', '.markdown':'text/markdown', '.txt':'text/plain',
        '.mp3':'audio/mpeg', '.wav':'audio/wav', '.m4a':'audio/mp4', '.ogg':'audio/ogg', '.flac':'audio/flac',
        '.mp4':'video/mp4', '.m4v':'video/mp4', '.mov':'video/quicktime', '.webm':'video/webm',
        '.png':'image/png', '.jpg':'image/jpeg', '.jpeg':'image/jpeg', '.gif':'image/gif', '.webp':'image/webp'}


class PushError(Exception):
    def __init__(self, code, message, status=0, uncertain=False):
        self.code, self.message, self.status, self.uncertain = code, message, status, uncertain
        super().__init__(message)


def encode(value):
    try:
        return json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(',', ':')).encode('utf-8')
    except (ValueError, TypeError, UnicodeError):
        raise PushError('invalid_json', 'Input must contain valid UTF-8 JSON values.') from None


def read_json(path):
    try:
        return json.loads(Path(path).read_text(encoding='utf-8-sig'))
    except (OSError, ValueError, UnicodeError):
        raise PushError('invalid_input', 'Cannot read the specified UTF-8 JSON file.') from None


def endpoint(value):
    try:
        p = urlsplit(value)
        if p.scheme not in ('http', 'https') or not p.hostname or p.username or p.password or p.query or p.fragment:
            raise ValueError()
        if any(ord(c) < 33 for c in value) or p.port == 0:
            raise ValueError()
        if p.scheme == 'http':
            host = p.hostname.lower()
            try:
                loopback = ipaddress.ip_address(host).is_loopback
            except ValueError:
                loopback = host == 'localhost'
            if not loopback:
                raise ValueError()
        path = p.path.rstrip('/')
        if path.endswith('/api/v1'):
            path = path[:-7]
        if any(segment in ('.', '..') for segment in path.split('/')):
            raise ValueError()
        return urlunsplit((p.scheme, p.netloc, path, '', '')).rstrip('/')
    except (ValueError, TypeError):
        raise PushError('invalid_endpoint', 'Use HTTPS, or loopback HTTP, without credentials/query/fragment.') from None


def source_uuid(value):
    try:
        return str(UUID(str(value)))
    except (ValueError, TypeError, AttributeError):
        raise PushError('invalid_source', 'source_id must be the dedicated client UUID.') from None


def credentials(config=None, base_url=None):
    """Choose one complete credential pair; never read application admin files."""
    env_id, env_key = os.environ.get('WZSTUDY_SOURCE_ID'), os.environ.get('WZSTUDY_SOURCE_KEY')
    if env_id or env_key:
        if not env_id or not env_key:
            raise PushError('missing_credentials', 'Set both WZSTUDY_SOURCE_ID and WZSTUDY_SOURCE_KEY.')
        url = endpoint(base_url or os.environ.get('WZSTUDY_BASE_URL', 'http://127.0.0.1:8091'))
        source_id, key = env_id, env_key
    else:
        filename = config or os.environ.get('WZSTUDY_CONFIG')
        if not filename:
            raise PushError('missing_credentials', 'Provide --config/WZSTUDY_CONFIG, or the dedicated source environment pair.')
        cfg = read_json(filename)
        if not isinstance(cfg, dict):
            raise PushError('invalid_config', 'Client configuration must be an object.')
        url = endpoint(cfg.get('base_url', ''))
        if base_url and endpoint(base_url) != url:
            raise PushError('endpoint_mismatch', 'Config is bound to its endpoint; create a separate config rather than forwarding its secret.')
        source_id, key = cfg.get('source_id'), cfg.get('source_key')
    if not isinstance(key, str) or not key.startswith('wzsk_') or not key.isascii() or len(key) > 256 or any(c.isspace() or ord(c) < 33 for c in key):
        raise PushError('invalid_credentials', 'A source-specific wzsk_ credential is required, not an administrator key/password.')
    return url, source_uuid(source_id), key


class Client:
    def __init__(self, base_url, source_id='', key='', timeout=60):
        self.base_url = endpoint(base_url)
        self.parts = urlsplit(self.base_url)
        self.source_id, self.key, self.timeout = source_id, key, timeout

    def request(self, path, method='GET', body=None, content_type='application/json', length=None, auth=True):
        # http.client neither follows redirects nor consumes proxy environment variables.
        if self.parts.scheme == 'https':
            conn = http.client.HTTPSConnection(self.parts.hostname, self.parts.port, timeout=self.timeout, context=ssl.create_default_context())
        else:
            conn = http.client.HTTPConnection(self.parts.hostname, self.parts.port, timeout=self.timeout)
        headers = {'Accept': 'application/json', 'User-Agent': 'wzstudy-push-skill/1.0'}
        if auth and self.key:
            headers['Authorization'] = 'Bearer ' + self.key
        if body is not None:
            headers['Content-Type'] = content_type
            headers['Content-Length'] = str(length if length is not None else len(body))
        writing = method not in ('GET', 'HEAD')
        try:
            conn.request(method, self.parts.path + '/api/v1' + path, body=body, headers=headers)
            response = conn.getresponse()
            raw = response.read(16 * 1024 * 1024 + 1)
            status = response.status
        except (OSError, http.client.HTTPException, ssl.SSLError):
            raise PushError('network_error', 'Request interrupted. A write may have committed; no automatic retry was performed.', uncertain=writing) from None
        finally:
            conn.close()
        if 300 <= status < 400:
            raise PushError('redirect_refused', 'Redirect refused; credentials were not forwarded.', status)
        if len(raw) > 16 * 1024 * 1024:
            raise PushError('response_too_large', 'Unexpectedly large response.', status, writing)
        try:
            result = json.loads(raw)
        except (ValueError, UnicodeError):
            raise PushError('invalid_response', 'Server/proxy did not return JSON; check status before resubmitting.', status, writing) from None
        if not 200 <= status < 300:
            code = result.get('error', {}).get('code', 'http_error') if isinstance(result, dict) else 'http_error'
            safe = {400:'Input rejected; check field limits and metadata.', 401:'Source credential was rejected.',
                    403:'Source is disabled or origin is denied.',404:'Endpoint or source was not found.',
                    409:'Conflict; inspect current data before retrying.',413:'Payload exceeds the server limit.',
                    429:'Rate limited; retry only after reviewing the response.'}.get(status, 'Server operation failed; confirm current state before another write.')
            # Do not echo server bodies, which may contain collected private material.
            raise PushError(str(code), safe, status, writing and status >= 500)
        if not isinstance(result, dict) or 'data' not in result:
            raise PushError('invalid_response', 'Response is missing its data envelope.', status, writing)
        return result['data']

    def doctor(self):
        ready = self.request('/health/ready', auth=False)
        status = self.request('/ingest/' + self.source_id + '/status')
        if not isinstance(status, dict) or status.get('source_id') != self.source_id or status.get('enabled') is not True or not isinstance(status.get('permissions'), list) or 'ingest' not in status['permissions']:
            raise PushError('invalid_response', 'Ingestion status did not confirm this source and its ingest permission.')
        return {'ok': True, 'base_url': self.base_url, 'source_id': self.source_id,
                'health': ready.get('status'), 'source_authenticated': True,
                'permissions': status['permissions'], 'auto_publish': status.get('auto_publish', False),
                'note': 'No materials were submitted; no inbox or reading permission is required.'}


def normalize(item, with_files=False):
    if not isinstance(item, dict) or set(item) - FIELDS:
        raise PushError('invalid_item', 'Each item must be an object using only supported Content fields.')
    value = dict(item)
    for key, limit in STRING_LIMITS.items():
        if key in value:
            if not isinstance(value[key], str) or len(value[key].encode('utf-8')) > limit:
                raise PushError('invalid_item', f'{key} must be text within {limit} UTF-8 bytes.')
    if not value.get('title', '').strip():
        raise PushError('invalid_item', 'A factual, non-empty title is required.')
    value['title'] = value['title'].strip()
    value.setdefault('kind', 'article')
    value.setdefault('format', 'markdown')
    if value['kind'] not in ('article', 'audio', 'video', 'carousel') or value['format'] not in ('text', 'markdown'):
        raise PushError('invalid_item', 'Use article/audio/video/carousel and text/markdown, not raw HTML.')
    if value.get('url'):
        try:
            p = urlsplit(value['url'])
            if p.scheme not in ('http', 'https') or not p.hostname or p.username or p.password or any(ord(c) < 33 for c in value['url']):
                raise ValueError()
            value['url'] = urlunsplit((p.scheme, p.netloc, p.path, p.query, ''))
        except ValueError:
            raise PushError('invalid_item', 'Source URL must be HTTP(S) without embedded credentials.') from None
    if 'tags' in value:
        if not isinstance(value['tags'], list) or len(value['tags']) > 20 or any(not isinstance(t, str) or not t.strip() or len(t.encode('utf-8')) > 80 for t in value['tags']):
            raise PushError('invalid_item', 'At most 20 non-empty tags, each within 80 UTF-8 bytes.')
        value['tags'] = sorted(set(t.strip() for t in value['tags']))
    for key in ('published_at', 'source_updated_at'):
        if value.get(key) is not None:
            try:
                date = datetime.fromisoformat(value[key].replace('Z', '+00:00'))
                if date.tzinfo is None or date.year < 1970:
                    raise ValueError()
            except (AttributeError, ValueError, TypeError):
                raise PushError('invalid_item', f'{key} must be a timezone-aware RFC3339 timestamp.') from None
    if not with_files and value['kind'] != 'article' and not value.get('url') and not value.get('assets'):
        raise PushError('invalid_item', 'Non-article JSON requires an external media URL or registered assets; use upload for local files.')
    if not value.get('external_id', '').strip():
        value.pop('external_id', None)
        identity = value.get('url') or encode(value).decode('utf-8')
        value['external_id'] = 'ai:' + hashlib.sha256(identity.encode('utf-8')).hexdigest()
    return value


def load_items(path):
    if str(path).lower().endswith('.jsonl'):
        try:
            data = [json.loads(line) for line in Path(path).read_text(encoding='utf-8-sig').splitlines() if line.strip()]
        except (ValueError, OSError, UnicodeError):
            raise PushError('invalid_input', 'Invalid UTF-8 JSONL input.') from None
    else:
        data = read_json(path)
    return normalize_items(data)


def normalize_items(data):
    if isinstance(data, dict):
        data = data.get('items', [data])
    if not isinstance(data, list) or not data:
        raise PushError('invalid_input', 'Provide one Content object, an array, {items:[...]}, or JSONL.')
    items = [normalize(item) for item in data]
    ids = [item['external_id'] for item in items]
    if len(ids) != len(set(ids)):
        raise PushError('duplicate_external_id', 'Input contains duplicate external_id values; reconcile them before submitting.')
    return items


def batches(items):
    result, current = [], []
    for item in items:
        if len(encode({'items': [item]})) > MAX_JSON:
            raise PushError('payload_too_large', 'One item exceeds the JSON request limit.')
        if current and (len(current) >= 100 or len(encode({'items': current + [item]})) > MAX_JSON):
            result.append(current)
            current = []
        current.append(item)
    if current:
        result.append(current)
    return result


def receipt(data):
    return {key: data.get(key, 0) for key in ('created', 'updated', 'unchanged', 'published')} | {key: data[key] for key in ('receipt_id', 'source_id', 'received_at') if key in data} | {
        'items': [{'id': x.get('id'), 'external_id': x.get('external_id'), 'revision': x.get('revision'),
                   'status': x.get('status', 'pending')} for x in data.get('items', [])]}


def upload(client, metadata, paths, dry_run=False):
    if not 1 <= len(paths) <= 20:
        raise PushError('invalid_upload', 'Provide 1-20 local files.')
    files = [Path(p) for p in paths]
    if any(not p.is_file() or p.stat().st_size == 0 or p.suffix.lower() not in MIME for p in files):
        raise PushError('invalid_upload', 'Use non-empty supported media/text files; HTML and SVG are not accepted.')
    if sum(p.stat().st_size for p in files) > MAX_UPLOAD:
        raise PushError('payload_too_large', 'Default upload cap is 200 MiB total.')
    raw = dict(metadata)
    if 'kind' not in raw:
        mime = MIME[files[0].suffix.lower()]
        raw['kind'] = 'carousel' if mime.startswith('image/') else mime.split('/')[0] if mime.startswith(('audio/', 'video/')) else 'article'
    if not raw.get('external_id'):
        digest = hashlib.sha256()
        for path in files:
            with path.open('rb') as stream:
                while chunk := stream.read(1024 * 1024):
                    digest.update(chunk)
            digest.update(b'\0')
        raw['external_id'] = 'ai-file:' + digest.hexdigest()
    item = normalize(raw, with_files=True)
    if item['kind'] != 'carousel' and len(files) != 1:
        raise PushError('invalid_upload', 'Audio, video and article accept one file; use carousel for multiple images.')
    expected = {'audio':'audio/', 'video':'video/', 'carousel':'image/', 'article':'text/'}[item['kind']]
    if any(not MIME[p.suffix.lower()].startswith(expected) for p in files):
        raise PushError('invalid_upload', 'File extensions do not match the declared content kind.')
    for path in files:
        if any(ord(c) < 32 for c in path.name) or '"' in path.name or '\\' in path.name:
            raise PushError('invalid_upload', 'Unsafe file name.')
    if dry_run:
        return {'dry_run': True, 'files': len(files), 'bytes': sum(p.stat().st_size for p in files), 'external_id': item['external_id'], 'kind': item['kind']}
    boundary = 'wzstudy-' + secrets.token_hex(16)
    with tempfile.SpooledTemporaryFile(max_size=1024 * 1024, mode='w+b') as stream:
        stream.write(('--' + boundary + '\r\nContent-Disposition: form-data; name="metadata"\r\nContent-Type: application/json\r\n\r\n').encode())
        stream.write(encode(item))
        stream.write(b'\r\n')
        for path in files:
            name = path.name
            if any(ord(c) < 32 for c in name) or '"' in name or '\\' in name:
                raise PushError('invalid_upload', 'Unsafe file name.')
            stream.write(('--' + boundary + '\r\nContent-Disposition: form-data; name="files"; filename="' + name + '"\r\nContent-Type: ' + MIME[path.suffix.lower()] + '\r\n\r\n').encode('utf-8'))
            with path.open('rb') as source:
                while chunk := source.read(1024 * 1024):
                    stream.write(chunk)
            stream.write(b'\r\n')
        stream.write(('--' + boundary + '--\r\n').encode())
        length = stream.tell()
        stream.seek(0)
        return receipt(client.request('/ingest/' + client.source_id + '/files', 'POST', stream, 'multipart/form-data; boundary=' + boundary, length))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', help='Private source config path (or WZSTUDY_CONFIG).')
    parser.add_argument('--base-url', help='HTTPS or loopback HTTP endpoint; cannot override a config-bound origin.')
    parser.add_argument('--timeout', type=float, default=60)
    commands = parser.add_subparsers(dest='command', required=True)
    commands.add_parser('doctor', help='Read-only health and dedicated-source authentication check.')
    push = commands.add_parser('push')
    push.add_argument('--input', required=True)
    push.add_argument('--dry-run', action='store_true')
    media = commands.add_parser('upload')
    media.add_argument('--metadata', required=True)
    media.add_argument('--file', action='append', required=True)
    media.add_argument('--dry-run', action='store_true')
    args = parser.parse_args(argv)
    successful = []
    try:
        if not 0 < args.timeout <= 600:
            raise PushError('invalid_timeout', 'Timeout must be 1-600 seconds.')
        dry = getattr(args, 'dry_run', False)
        if args.command == 'push':
            groups = batches(load_items(args.input))
            if dry:
                result = {'dry_run': True, 'items': sum(map(len, groups)), 'batches': len(groups), 'external_ids': [i['external_id'] for g in groups for i in g], 'network_requests': 0}
            else:
                client = Client(*credentials(args.config, args.base_url), timeout=args.timeout)
                for group in groups:
                    successful.append(receipt(client.request('/ingest/' + client.source_id, 'POST', encode({'items': group}))))
                result = {'ok': True, 'source_id': client.source_id, 'batches': successful,
                          'note': 'Publication is controlled by source policy; inspect published count. This client does not approve content or create push plans.'}
        elif args.command == 'upload':
            metadata = read_json(args.metadata)
            if not isinstance(metadata, dict):
                raise PushError('invalid_input', 'Upload metadata must be one Content object.')
            client = None if dry else Client(*credentials(args.config, args.base_url), timeout=args.timeout)
            result = upload(client, metadata, args.file, dry)
        else:
            result = Client(*credentials(args.config, args.base_url), timeout=args.timeout).doctor()
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0
    except (PushError, OSError, UnicodeError) as error:
        if not isinstance(error, PushError):
            error = PushError('local_file_error', 'Cannot access a specified local file safely.')
        print(json.dumps({'ok': False, 'error': {'code': error.code, 'message': error.message, 'status': error.status,
              'commit_uncertain': error.uncertain}, 'completed_batches': successful}, ensure_ascii=False), file=sys.stderr)
        return 4 if error.uncertain else 2


if __name__ == '__main__':
    if hasattr(sys.stdout, 'reconfigure'):
        sys.stdout.reconfigure(encoding='utf-8')
        sys.stderr.reconfigure(encoding='utf-8')
    raise SystemExit(main())
