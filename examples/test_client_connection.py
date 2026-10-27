import copy
from email.message import Message
import io
import json
import unittest
from unittest.mock import patch
import urllib.error
import uuid

import client_connection as connection

KEY = 'wzck_' + 'q' * 43
IDENTIFIER = str(uuid.uuid4())


def payload(**changes):
    value = {'type': connection.TYPE, 'version': 1, 'base_url': 'https://study.example.test',
             'client_id': IDENTIFIER, 'name': '手机学习端 / 示例', 'access_key': KEY}
    value.update(changes)
    return json.dumps(value, ensure_ascii=False)


def identity(**changes):
    value = {'type': connection.TYPE, 'version': 1, 'client_id': IDENTIFIER,
             'name': '当前服务名称', 'permissions': ['read', 'sync']}
    value.update(changes)
    return value


class Response(io.BytesIO):
    def __init__(self, value):
        super().__init__(json.dumps({'data': value}, ensure_ascii=False).encode())
        self.status = 200
        self.headers = Message()
        self.headers['Content-Type'] = 'application/json; charset=utf-8'


class ConnectionTests(unittest.TestCase):
    def test_unicode_parse_and_redacted_representation(self):
        config = connection.parse_connection(payload())
        self.assertEqual(config.client_id, IDENTIFIER)
        self.assertEqual(config.name, '手机学习端 / 示例')
        self.assertNotIn(KEY, repr(config))
        self.assertNotIn(KEY, json.dumps(config.summary()))
        self.assertNotIn(KEY, repr(connection.parse_connection(payload(name=KEY))))
        self.assertEqual(config.headers()['Authorization'], 'Bearer ' + KEY)

    def test_root_url_canonicalization(self):
        for raw, want in [('HTTPS://STUDY.EXAMPLE.TEST:443/', 'https://study.example.test'),
                          ('http://127.0.0.1:8091/', 'http://127.0.0.1:8091'),
                          ('http://１２７.０.０.１:8091', 'http://127.0.0.1:8091'),
                          ('https://[::1]:443', 'https://[::1]'),
                          ('https://温知.example', 'https://xn--d6wq1s.example')]:
            with self.subTest(raw=raw):
                self.assertEqual(connection.normalize_base_url(raw), want)

    def test_unsafe_destinations_rejected(self):
        for address in ['file:///tmp/config', 'javascript:alert(1)', '//example.test',
                        'https://user:secret@example.test', 'https://example.test/api/v1',
                        'https://example.test/admin/', 'https://example.test/?key=x',
                        'https://example.test/#x', 'https://example.test:70000',
                        'https://example.test:0', 'http://0.0.0.0:8091', 'http://[::]',
                        'http://０.０.０.０', 'http://１２７.１', 'http://0x00000000',
                        'http://0x7f.0.0.1', 'http://127.0.0.01', 'http://[::ffff:0:0]',
                        'https://exa\nmple.test', 'https://example.test\\other', 'https://%31%32%37.0.0.1']:
            with self.subTest(address=address):
                with self.assertRaises(connection.ConnectionError):
                    connection.parse_connection(payload(base_url=address))

    def test_type_version_and_fields_are_strict(self):
        for changes in [{'version': True}, {'version': 2}, {'version': '1'}, {'type': 'wzstudy.source'},
                        {'permissions': ['read']}, {'access_key': ''}, {'client_id': 'not-a-uuid'},
                        {'name': ''}, {'name': 'name\ncontrol'}]:
            with self.subTest(changes=list(changes)):
                with self.assertRaises(connection.ConnectionError):
                    connection.parse_connection(payload(**changes))
        for text in ['[]', '{}', '{"version":NaN}', '{"type":1,"type":2}', 'x' * 1201]:
            with self.assertRaises(connection.ConnectionError):
                connection.parse_connection(text)

    def test_key_header_and_payload_bounds(self):
        for key in ['short', KEY + '\r\nX-Injected: value', 'k' * 257, ' ' + KEY]:
            with self.assertRaises(connection.ConnectionError):
                connection.parse_connection(payload(access_key=key))
        config = connection.parse_connection(payload(access_key='wzsk_' + 'l' * 43))
        self.assertTrue(config.access_key.startswith('wzsk_'))
        # Old prefixes are not proof of source/query authorization; online identity is authoritative.

    def test_online_identity_uses_only_bearer_and_fixed_endpoint(self):
        config = connection.parse_connection(payload())
        with patch.object(connection.urllib.request, 'build_opener') as factory:
            factory.return_value.open.return_value = Response(identity())
            out = connection.verify_connection(config)
            request = factory.return_value.open.call_args.args[0]
            self.assertEqual(request.full_url, config.base_url + '/api/v1/client/connection')
            self.assertEqual(request.get_header('Authorization'), 'Bearer ' + KEY)
            self.assertNotIn('Cookie', request.headers)
            self.assertEqual(out['client_id'], IDENTIFIER)
            self.assertNotIn(KEY, repr(out))

    def test_http_requires_explicit_local_development_consent(self):
        config = connection.parse_connection(payload(base_url='http://127.0.0.1:8091'))
        with patch.object(connection.urllib.request, 'build_opener') as factory:
            with self.assertRaises(connection.ConnectionError):
                connection.verify_connection(config)
            factory.assert_not_called()
            factory.return_value.open.return_value = Response(identity(permissions=['sync']))
            self.assertEqual(connection.verify_connection(config, allow_insecure_http=True)['permissions'], ['sync'])

    def test_online_wrong_identity_and_nonquery_permissions_rejected(self):
        invalid = [identity(client_id=str(uuid.uuid4())), identity(type='wzstudy.source'),
                   identity(version=True), identity(permissions=[]), identity(permissions=['feedback']), identity(permissions=['ingest']),
                   identity(permissions=['read', 'upload']), identity(name='')]
        for value in invalid:
            with self.subTest(fields=sorted(value)):
                with patch.object(connection.urllib.request, 'build_opener') as factory:
                    factory.return_value.open.return_value = Response(value)
                    with self.assertRaises(connection.ConnectionError):
                        connection.verify_connection(connection.parse_connection(payload()))

    def test_feedback_capability_does_not_change_pairing_format(self):
        config = connection.parse_connection(payload())
        with patch.object(connection.urllib.request, 'build_opener') as factory:
            factory.return_value.open.return_value = Response(identity(permissions=['read', 'sync', 'feedback']))
            self.assertIn('feedback', connection.verify_connection(config)['permissions'])

    def test_redirect_and_server_failures_never_fallback_or_echo_key(self):
        config = connection.parse_connection(payload())
        redirect = connection._NoRedirect()
        self.assertIsNone(redirect.redirect_request(None, None, 302, '', {}, 'https://other.example'))
        for status in [302, 401, 403, 404, 503]:
            with patch.object(connection.urllib.request, 'build_opener') as factory:
                factory.return_value.open.side_effect = urllib.error.HTTPError(config.base_url, status, KEY, {}, io.BytesIO(KEY.encode()))
                with self.assertRaises(connection.ConnectionError) as error:
                    connection.verify_connection(config)
                self.assertNotIn(KEY, str(error.exception))
                self.assertEqual(factory.return_value.open.call_count, 1)

    def test_registry_add_duplicate_and_confirmed_key_update(self):
        registry = connection.ConnectionRegistry()
        with patch.object(connection, 'verify_connection', return_value={'client_id': IDENTIFIER, 'name': '当前服务名称', 'permissions': ['read']}):
            first = registry.add_from_qr(payload())
            self.assertTrue(first['added'])
            duplicate = registry.add_from_qr(payload())
            self.assertFalse(duplicate['added'])
            self.assertFalse(duplicate['updated'])
            newer = payload(access_key='wzck_' + 'n' * 43)
            with self.assertRaises(connection.ConnectionError):
                registry.add_from_qr(newer)
            self.assertEqual(registry.get('https://study.example.test', IDENTIFIER).access_key, KEY)
            self.assertTrue(registry.add_from_qr(newer, replace_existing=True)['updated'])
            self.assertEqual(registry.get('https://study.example.test', IDENTIFIER).access_key, 'wzck_' + 'n' * 43)

    def test_failed_identity_never_adds_or_overwrites(self):
        registry = connection.ConnectionRegistry()
        with patch.object(connection, 'verify_connection', side_effect=connection.ConnectionError('denied')):
            with self.assertRaises(connection.ConnectionError):
                registry.add_from_qr(payload())
        self.assertIsNone(registry.get('https://study.example.test', IDENTIFIER))

    def test_distinct_origins_are_not_merged_by_client_id(self):
        registry = connection.ConnectionRegistry()
        with patch.object(connection, 'verify_connection', return_value={'client_id': IDENTIFIER, 'name': 'one', 'permissions': ['read']}):
            registry.add_from_qr(payload())
            registry.add_from_qr(payload(base_url='https://other.example.test'))
        self.assertIsNotNone(registry.get('https://study.example.test', IDENTIFIER))
        self.assertIsNotNone(registry.get('https://other.example.test', IDENTIFIER))


if __name__ == '__main__':
    unittest.main()
