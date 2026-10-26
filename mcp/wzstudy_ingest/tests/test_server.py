import io
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from server import Adapter, LEGACY, META_CAPS, META_INFO, META_VERSION, MODERN, PushError, serve


class FakeClient:
    source_id = '00000000-0000-4000-8000-000000000001'

    def __init__(self):
        self.calls = []
        self.error = None

    def doctor(self):
        return {'source_authenticated': True, 'permissions': ['ingest', 'upload'], 'auto_publish': False}

    def request(self, *args):
        self.calls.append(args)
        if self.error:
            raise self.error
        return {'created': 1, 'updated': 0, 'unchanged': 0, 'published': 0,
                'receipt_id': 'receipt-1', 'source_id': self.source_id,
                'items': [{'id': 'material-1', 'external_id': 'lesson', 'revision': 1, 'status': 'pending'}]}


def modern(method, params=None, identifier=1, version=MODERN):
    return {'jsonrpc': '2.0', 'id': identifier, 'method': method, 'params': {
        '_meta': {META_VERSION: version, META_INFO: {'name': 'test-client', 'version': '1'}, META_CAPS: {}}, **(params or {})}}


class MCPTests(unittest.TestCase):
    def setUp(self):
        self.client = FakeClient()
        self.adapter = Adapter(self.client)

    def test_modern_discovery_and_stateless_tools(self):
        out = self.adapter.dispatch(modern('server/discover'))['result']
        self.assertEqual(out['resultType'], 'complete')
        self.assertIn(MODERN, out['supportedVersions'])
        tools = self.adapter.dispatch(modern('tools/list'))['result']['tools']
        self.assertEqual([v['name'] for v in tools], ['wzstudy_ingest_status', 'wzstudy_ingest_items'])
        self.assertNotIn('source_key', json.dumps(tools))
        self.assertNotIn('publish', [v['name'] for v in tools])
        # Each request must supply its own metadata; discovery is not a session.
        denied = self.adapter.dispatch({'jsonrpc': '2.0', 'id': 3, 'method': 'tools/list'})
        self.assertEqual(denied['error']['code'], -32600)

    def test_legacy_initialization_and_notification(self):
        for version in LEGACY:
            with self.subTest(version=version):
                adapter = Adapter(self.client)
                out = adapter.dispatch({'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {
                    'protocolVersion': version, 'capabilities': {}, 'clientInfo': {'name': 'test', 'version': '1'}}})
                self.assertEqual(out['result']['protocolVersion'], version)
                self.assertNotIn('resultType', out['result'])
                self.assertIsNone(adapter.dispatch({'jsonrpc': '2.0', 'method': 'notifications/initialized'}))
                self.assertIn('tools', adapter.dispatch({'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list'})['result'])
                status = adapter.dispatch({'jsonrpc': '2.0', 'id': 3, 'method': 'tools/call', 'params': {'name': 'wzstudy_ingest_status', 'arguments': {}}})['result']
                self.assertTrue(json.loads(status['content'][0]['text'])['source_authenticated'])
                if version == '2025-03-26':
                    self.assertNotIn('structuredContent', status)
                self.assertEqual(adapter.dispatch(modern('tools/list'))['result']['resultType'], 'complete')

    def test_unsupported_version_and_invalid_ids(self):
        out = self.adapter.dispatch(modern('tools/list', version='1900-01-01'))
        self.assertEqual(out['error']['code'], -32022)
        self.assertIn(MODERN, out['error']['data']['supported'])
        for version in (None, {}, 'wzsk_must_not_echo'):
            invalid = self.adapter.dispatch(modern('tools/list', version=version))
            self.assertEqual(invalid['error']['code'], -32602)
            self.assertNotIn('wzsk_must_not_echo', json.dumps(invalid))
        for identifier in (None, True, 1.2):
            self.assertEqual(self.adapter.dispatch(modern('tools/list', identifier=identifier))['error']['code'], -32600)

    def test_no_endpoint_or_credential_override(self):
        out = self.adapter.dispatch(modern('tools/call', {'name': 'wzstudy_ingest_items', 'arguments': {
            'items': [{'title': 'lesson'}], 'source_key': 'must-not-be-echoed'}}))
        self.assertEqual(out['error']['code'], -32602)
        self.assertNotIn('must-not-be-echoed', json.dumps(out))
        self.assertEqual(self.client.calls, [])

    def test_real_receipt_and_atomic_batch_validation(self):
        args = {'items': [{'external_id': 'lesson', 'title': 'Lesson', 'content': 'body'}]}
        out = self.adapter.dispatch(modern('tools/call', {'name': 'wzstudy_ingest_items', 'arguments': args}))['result']
        self.assertFalse(out['isError'])
        self.assertEqual(out['structuredContent']['receipt_id'], 'receipt-1')
        self.assertEqual(out['structuredContent']['published'], 0)
        self.assertEqual(len(self.client.calls), 1)
        for items in ([{'external_id': 'same', 'title': '1'}, {'external_id': 'same', 'title': '2'}], [{'title': 'x'}] * 101):
            rejected = self.adapter.dispatch(modern('tools/call', {'name': 'wzstudy_ingest_items', 'arguments': {'items': items}}))
            self.assertTrue(rejected['result']['isError'])
        self.assertEqual(len(self.client.calls), 1)

    def test_dry_run_is_offline_and_uncertain_write_not_retried(self):
        args = {'items': [{'title': 'Lesson'}], 'dry_run': True}
        out = self.adapter.dispatch(modern('tools/call', {'name': 'wzstudy_ingest_items', 'arguments': args}))
        self.assertTrue(out['result']['structuredContent']['dry_run'])
        self.assertEqual(self.client.calls, [])
        args['dry_run'] = False
        self.client.error = PushError('network_error', 'Submission outcome is uncertain.', uncertain=True)
        out = self.adapter.dispatch(modern('tools/call', {'name': 'wzstudy_ingest_items', 'arguments': args}))
        self.assertTrue(out['result']['isError'])
        self.assertTrue(out['result']['structuredContent']['commit_uncertain'])
        self.assertEqual(len(self.client.calls), 1)

    def test_files_require_roots_and_reject_traversal(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / 'allowed'
            root.mkdir()
            permitted = root / 'lesson.txt'
            permitted.write_text('body', encoding='utf-8')
            outside = Path(temp) / 'private.txt'
            outside.write_text('private', encoding='utf-8')
            adapter = Adapter(self.client, [root])
            self.assertFalse(next(t for t in adapter.tools() if t['name'] == 'wzstudy_upload_files')['annotations']['idempotentHint'])
            self.assertEqual(adapter.files([str(permitted)]), [permitted.resolve()])
            for value in [str(outside), str(root / '..' / 'private.txt')]:
                with self.assertRaises(PushError):
                    adapter.files([value])
            args = {'metadata': {'title': 'Lesson'}, 'files': [str(permitted)], 'dry_run': True}
            out = adapter.dispatch(modern('tools/call', {'name': 'wzstudy_upload_files', 'arguments': args}))
            self.assertTrue(out['result']['structuredContent']['dry_run'])
            self.assertEqual(self.client.calls, [])
            self.assertEqual(self.adapter.dispatch(modern('tools/call', {'name': 'wzstudy_upload_files', 'arguments': args}))['error']['code'], -32602)

    def test_stdio_messages_only_and_notifications_do_not_write(self):
        notification = {'jsonrpc': '2.0', 'method': 'tools/call', 'params': {'name': 'wzstudy_ingest_items', 'arguments': {'items': [{'title': 'must not write'}]}}}
        messages = [notification, modern('tools/list'), []]
        incoming = io.BytesIO(b'not-json\n' + b''.join(json.dumps(m).encode() + b'\n' for m in messages))
        outgoing = io.StringIO()
        serve(self.adapter, incoming, outgoing)
        results = [json.loads(line) for line in outgoing.getvalue().splitlines()]
        self.assertEqual(len(results), 3)
        self.assertEqual(results[0]['error']['code'], -32700)
        self.assertIn('tools', results[1]['result'])
        self.assertEqual(results[2]['error']['code'], -32600)
        self.assertEqual(self.client.calls, [])


if __name__ == '__main__':
    unittest.main()
