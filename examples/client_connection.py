"""WzStudy QR text import reference (standard library, no camera or disk key store).

After a scanner decodes UTF-8 text, review its destination, then call
ConnectionRegistry.add_from_qr(). It validates the Key's real client identity
before adding/updating an in-memory connection. A real app should replace that
memory registry with its OS secure credential store, not logs/localStorage.
"""
from dataclasses import dataclass, field, replace
import argparse
import ipaddress
import json
from pathlib import Path
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid

TYPE = 'wzstudy.query_client'
VERSION = 1
MAX_BYTES = 1200
FIELDS = {'type', 'version', 'base_url', 'client_id', 'name', 'access_key'}


class ConnectionError(ValueError):
    """Safe message; never includes QR text, credentials or remote error bodies."""


def _canonical_host(host):
    # Map IDNA first: fullwidth digits/dots can become numeric IP spellings.
    candidate = host if ':' in host else host.encode('idna').decode('ascii').lower()
    try:
        address = ipaddress.ip_address(candidate)
    except ValueError:
        numeric_part = r'(?:0[xX][0-9a-fA-F]+|[0-9]+)'
        if re.fullmatch(numeric_part + r'(?:\.' + numeric_part + r')*\.?', candidate):
            raise ConnectionError('Ambiguous numeric host is not supported.')
        if not re.fullmatch(r'[a-z0-9._-]+', candidate) or len(candidate) > 253:
            raise ConnectionError('Invalid host.')
        return candidate
    mapped = address.ipv4_mapped if address.version == 6 else None
    if address.is_unspecified or mapped is not None and mapped.is_unspecified:
        raise ConnectionError('Wildcard listening addresses are not client destinations.')
    return '[' + address.compressed + ']' if address.version == 6 else str(address)


def normalize_base_url(value):
    if not isinstance(value, str) or not value or re.search(r'[\s\\\x00-\x1f\x7f%?#]', value):
        raise ConnectionError('Invalid service root URL.')
    try:
        parsed = urllib.parse.urlsplit(value)
        port = parsed.port
        if (parsed.scheme.lower() not in ('http', 'https') or not parsed.hostname or parsed.username is not None
                or parsed.password is not None or parsed.path not in ('', '/') or parsed.query or parsed.fragment
                or port == 0):
            raise ValueError()
        host = _canonical_host(parsed.hostname)
        scheme = parsed.scheme.lower()
        suffix = '' if port is None or port == (443 if scheme == 'https' else 80) else ':' + str(port)
        result = scheme + '://' + host + suffix
        if len(result.encode()) > 512:
            raise ValueError()
        return result
    except ConnectionError:
        raise
    except (ValueError, UnicodeError):
        raise ConnectionError('Invalid service root URL; no credentials, paths, query or fragment allowed.') from None


def _valid_name(value):
    if not isinstance(value, str) or not value.strip() or re.search(r'[\x00-\x1f\x7f]', value):
        return False
    try:
        return len(value.encode('utf-8')) <= 200
    except UnicodeError:
        return False


def _pairs(pairs):
    result = {}
    for name, value in pairs:
        if name in result:
            raise ConnectionError('Duplicate JSON fields are not supported.')
        result[name] = value
    return result


def _constant(_):
    raise ConnectionError('Non-standard JSON numbers are not supported.')


@dataclass(frozen=True, repr=False)
class ConnectionConfig:
    base_url: str
    client_id: str
    name: str
    access_key: str = field(repr=False)

    def __repr__(self):
        return 'ConnectionConfig(' + repr(self.summary()) + ', access_key=<redacted>)'

    def headers(self):
        return {'Authorization': 'Bearer ' + self.access_key, 'Accept': 'application/json'}

    def summary(self):
        return {'base_url': self.base_url, 'client_id': self.client_id,
                'name': self.name.replace(self.access_key, '[redacted]')}


def parse_connection(text):
    try:
        if not isinstance(text, str) or len(text.encode('utf-8')) > MAX_BYTES:
            raise ConnectionError('QR payload is not text or exceeds 1200 UTF-8 bytes.')
        value = json.loads(text, object_pairs_hook=_pairs, parse_constant=_constant)
        if (not isinstance(value, dict) or set(value) != FIELDS or value['type'] != TYPE
                or type(value['version']) is not int or value['version'] != VERSION):
            raise ConnectionError('Unsupported QR type/version or field set.')
        key, name, identifier = value['access_key'], value['name'], value['client_id']
        if not isinstance(key, str) or not re.fullmatch(r'[A-Za-z0-9_-]{32,256}', key):
            raise ConnectionError('Invalid query client Key format.')
        if not _valid_name(name):
            raise ConnectionError('Invalid client name.')
        if not isinstance(identifier, str):
            raise ConnectionError('Invalid client ID.')
        parsed_id = uuid.UUID(identifier)
        if parsed_id.int == 0 or str(parsed_id) != identifier.lower():
            raise ConnectionError('Invalid client ID.')
        base = normalize_base_url(value['base_url'])
        if key.lower() in base.lower():
            raise ConnectionError('Service URL must not contain the Key.')
        return ConnectionConfig(base, str(parsed_id), name, key)
    except ConnectionError:
        raise
    except (ValueError, TypeError, KeyError, UnicodeError, RecursionError):
        raise ConnectionError('Malformed QR configuration.') from None


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        return None


def verify_connection(config, *, allow_insecure_http=False, timeout=10):
    """Only call after the user has reviewed/confirmed the QR destination."""
    if not isinstance(config, ConnectionConfig):
        raise ConnectionError('Expected a parsed connection configuration.')
    if config.base_url.startswith('http://') and not allow_insecure_http:
        raise ConnectionError('HTTP is unencrypted; explicitly allow it only for trusted local development.')
    request = urllib.request.Request(config.base_url + '/api/v1/client/connection', headers=config.headers())
    opener = urllib.request.build_opener(_NoRedirect())
    try:
        with opener.open(request, timeout=timeout) as response:
            if response.status != 200 or response.headers.get_content_type() != 'application/json':
                raise ConnectionError('Invalid identity response.')
            data = response.read(65537)
            if len(data) > 65536:
                raise ConnectionError('Identity response exceeds the supported size.')
            value = json.loads(data.decode('utf-8'), object_pairs_hook=_pairs, parse_constant=_constant)['data']
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        raise ConnectionError('Connection verification failed with HTTP ' + str(status) + '; no redirect or credential fallback.') from None
    except (OSError, urllib.error.URLError):
        raise ConnectionError('Connection verification failed; check the confirmed destination and TLS.') from None
    except (ValueError, KeyError, TypeError, UnicodeError, RecursionError):
        raise ConnectionError('Invalid identity response.') from None
    if (not isinstance(value, dict) or value.get('type') != TYPE or type(value.get('version')) is not int
            or value['version'] != VERSION or value.get('client_id') != config.client_id
            or not _valid_name(value.get('name'))
            or not isinstance(value.get('permissions'), list) or not value['permissions']
            or any(p not in ('read', 'sync', 'feedback') for p in value['permissions'])
            or not any(p in ('read', 'sync') for p in value['permissions'])):
        raise ConnectionError('The Key does not identify the expected active query client.')
    return {'client_id': value['client_id'], 'name': value['name'], 'permissions': list(value['permissions'])}


class ConnectionRegistry:
    """Runnable in-memory example. Pair by origin + ID, never by display name."""
    def __init__(self):
        self._connections = {}

    def add_from_qr(self, text, *, replace_existing=False, allow_insecure_http=False):
        config = parse_connection(text)
        scope = (config.base_url, config.client_id)
        existing = self._connections.get(scope)
        if existing and existing.access_key != config.access_key and not replace_existing:
            raise ConnectionError('Existing connection has a different Key; explicit replacement is required.')
        identity = verify_connection(config, allow_insecure_http=allow_insecure_http)
        config = replace(config, name=identity['name'])
        self._connections[scope] = config
        return {**config.summary(), 'permissions': identity['permissions'], 'verified': True,
                'added': existing is None, 'updated': existing is not None and existing != config}

    def get(self, base_url, client_id):
        return self._connections.get((normalize_base_url(base_url), client_id))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', help='Scanner-decoded JSON file; otherwise read bounded UTF-8 from stdin. Never pass the Key as an argument.')
    parser.add_argument('--verify', action='store_true', help='Confirm the destination and make one read-only identity request.')
    parser.add_argument('--allow-insecure-http', action='store_true', help='Explicitly allow unencrypted HTTP for trusted local development.')
    args = parser.parse_args()
    try:
        if args.input:
            with Path(args.input).open('rb') as source:
                raw = source.read(MAX_BYTES + 1)
        else:
            raw = sys.stdin.buffer.read(MAX_BYTES + 1)
        config = parse_connection(raw.decode('utf-8'))
        if args.verify:
            result = ConnectionRegistry().add_from_qr(raw.decode('utf-8'), allow_insecure_http=args.allow_insecure_http)
        else:
            result = {**config.summary(), 'verified': False, 'note': 'Format checked only; review destination before online verification.'}
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0
    except (ConnectionError, OSError, UnicodeError):
        print('Connection import failed. Check protocol, confirmed destination, Key and permissions locally; no credentials printed.', file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.stdout.reconfigure(encoding='utf-8')
    raise SystemExit(main())
