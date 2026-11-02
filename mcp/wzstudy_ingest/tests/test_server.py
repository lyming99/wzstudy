import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock
from uuid import uuid4

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from server import (Adapter, LEGACY, MANAGEMENT_TOOLS, META_CAPS, META_INFO, META_VERSION, MODERN,
                    PushError, serve, workspace_credentials)


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


class FakeWorkspace:
    """Records workspace calls instead of touching a real server or database."""

    def __init__(self, error=None):
        self.calls = []
        self.error = error

    def request(self, path, method='GET', body=None):
        self.calls.append((path, method, body))
        if self.error:
            raise self.error
        if method == 'GET':
            return {'items': [], 'total': 0, 'limit': 20, 'offset': 0}
        return {'id': 'workspace-record', 'revision': 2}


class WorkspaceToolTests(unittest.TestCase):
    def setUp(self):
        self.client = FakeClient()
        self.workspace = FakeWorkspace()
        self.adapter = Adapter(self.client, (), self.workspace)

    def dispatch(self, name, arguments, adapter=None):
        return (adapter or self.adapter).dispatch(modern('tools/call', {'name': name, 'arguments': arguments}))

    def outcome(self, name, arguments, adapter=None):
        """Return (tool result, rpc error code). A protocol rejection yields (None, code)."""
        response = self.dispatch(name, arguments, adapter)
        if 'error' in response:
            return None, response['error']['code']
        return response['result'], None

    def payload(self, name, arguments, adapter=None):
        result, code = self.outcome(name, arguments, adapter)
        self.assertIsNone(code, 'unexpected protocol error')
        # The adapter reports failures with isError on the MCP result level while
        # the diagnostic fields live in structuredContent; merge for assertions.
        return {**result['structuredContent'], 'isError': result['isError']}

    def test_management_tools_hidden_without_user_session(self):
        plain = Adapter(self.client)
        names = [v['name'] for v in plain.tools()]
        self.assertEqual(names, ['wzstudy_ingest_status', 'wzstudy_ingest_items'])
        for name in MANAGEMENT_TOOLS:
            result, code = self.outcome(name, {'id': str(uuid4())}, plain)
            self.assertIsNone(result)
            self.assertEqual(code, -32602)

    def test_management_tools_listed_with_user_session(self):
        tools = {v['name']: v for v in self.adapter.tools()}
        self.assertEqual(set(MANAGEMENT_TOOLS) - set(tools), set())
        for name in ('wzstudy_catalog_list', 'wzstudy_catalog_get', 'wzstudy_roadmap_list', 'wzstudy_roadmap_get'):
            self.assertTrue(tools[name]['annotations']['readOnlyHint'])
        for name in ('wzstudy_catalog_delete', 'wzstudy_roadmap_delete'):
            self.assertTrue(tools[name]['annotations']['destructiveHint'])
        self.assertNotIn('user_token', json.dumps(self.adapter.tools()))

    def test_list_tools_use_pagination_and_query(self):
        self.payload('wzstudy_catalog_list', {'limit': 5, 'offset': 10})
        self.assertEqual(self.workspace.calls[-1][0], '/workspace/catalog/collections?limit=5&offset=10')
        self.payload('wzstudy_roadmap_list', {'q': 'Go 入门'})
        self.assertIn('q=Go+%E5%85%A5%E9%97%A8', self.workspace.calls[-1][0])
        bad = self.payload('wzstudy_catalog_list', {'limit': 0})
        self.assertTrue(bad['isError'])
        self.assertEqual(bad['code'], 'invalid_input')
        self.assertEqual(len(self.workspace.calls), 2)

    def test_catalog_save_create_and_update_rules(self):
        created = self.payload('wzstudy_catalog_save', {
            'title': ' 学习合集 ', 'summary': '简介',
            'chapters': [{'title': '第一章', 'item_id': str(uuid4())}], 'dry_run': True})
        self.assertEqual((created['method'], created['path']), ('POST', '/workspace/catalog/collections'))
        self.assertEqual(created['body']['revision'], 0)
        self.assertEqual(created['body']['title'], '学习合集')
        self.assertNotIn('id', created['body']['chapters'][0])
        self.assertEqual(self.workspace.calls, [])
        collection_id = str(uuid4())
        stale = self.payload('wzstudy_catalog_save', {'id': collection_id, 'title': '更新', 'dry_run': True})
        self.assertTrue(stale['isError'])
        self.assertEqual(stale['code'], 'invalid_input')
        updated = self.payload('wzstudy_catalog_save', {'id': collection_id, 'revision': 4, 'title': '更新', 'dry_run': True})
        self.assertEqual(updated['method'], 'PUT')
        self.assertTrue(updated['path'].endswith('/' + collection_id))
        missing = self.payload('wzstudy_catalog_save', {'title': '   ', 'dry_run': True})
        self.assertEqual(missing['code'], 'invalid_input')
        self.assertEqual(self.workspace.calls, [])

    def test_catalog_delete_requires_revision_and_real_call(self):
        result, code = self.outcome('wzstudy_catalog_delete', {'id': str(uuid4())})
        self.assertIsNone(result)
        self.assertEqual(code, -32602)
        collection_id = str(uuid4())
        out = self.payload('wzstudy_catalog_delete', {'id': collection_id, 'revision': 2})
        self.assertTrue(out['ok'])
        self.assertEqual(self.workspace.calls[0][0], '/workspace/catalog/collections/' + collection_id + '/delete')
        self.assertEqual(self.workspace.calls[0][1], 'POST')
        self.assertEqual(json.loads(self.workspace.calls[0][2]), {'revision': 2})

    def test_roadmap_nodes_normalized_from_parent_index(self):
        item = str(uuid4())
        body = self.payload('wzstudy_roadmap_save', {
            'title': 'Go 学习路线', 'summary': '从入门到实践',
            'nodes': [{'title': 'Go 学习路线'},
                      {'title': ' 基础语法 ', 'parent_index': 0, 'item_ids': [item], 'collapsed': True},
                      {'title': '并发', 'parent_index': 1}],
            'dry_run': True})['body']
        nodes = body['nodes']
        self.assertEqual(len(nodes), 3)
        self.assertEqual(len({node['id'] for node in nodes}), 3)
        self.assertEqual(nodes[0]['parent_id'], '')
        self.assertEqual(nodes[1]['parent_id'], nodes[0]['id'])
        self.assertEqual(nodes[2]['parent_id'], nodes[1]['id'])
        self.assertEqual(nodes[1]['title'], '基础语法')
        self.assertEqual(nodes[1]['item_ids'], [item])
        self.assertTrue(nodes[1]['collapsed'])
        self.assertFalse(nodes[0]['collapsed'])
        self.assertEqual(body['revision'], 0)
        self.assertEqual(self.workspace.calls, [])

    def test_roadmap_save_requires_revision_for_existing_record(self):
        roadmap_id = str(uuid4())
        stale = self.payload('wzstudy_roadmap_save', {
            'id': roadmap_id, 'title': '路线', 'nodes': [{'title': '路线'}], 'dry_run': True})
        self.assertTrue(stale['isError'])
        self.assertEqual(stale['code'], 'invalid_input')
        created = self.payload('wzstudy_catalog_save', {'title': '新合集', 'dry_run': True})
        self.assertEqual(created['method'], 'POST')
        self.assertEqual(self.workspace.calls, [])

    def test_roadmap_rejects_invalid_tree(self):
        duplicate = str(uuid4())
        for arguments in ({'title': '路线', 'nodes': [{'title': '别的名字'}]},
                          {'title': '路线', 'nodes': [{'title': '路线'}, {'title': '叉', 'parent_id': str(uuid4()), 'parent_index': 0}]},
                          {'title': '路线', 'nodes': [{'title': '路线'}, {'title': '重复', 'parent_index': 0, 'item_ids': [duplicate, duplicate]}]},
                          {'title': '路线', 'nodes': [{'title': '路线'}, {'title': '又一个根'}]},
                          {'title': '路线', 'nodes': [{'title': '路线', 'note': 'x' * 4001}]},
                          {'title': '路线', 'nodes': []}):
            payload = self.payload('wzstudy_roadmap_save', dict(arguments, dry_run=True))
            self.assertTrue(payload['isError'], arguments)
            self.assertEqual(payload['code'], 'invalid_input')
        self.assertEqual(self.workspace.calls, [])

    def test_workspace_auth_failure_reports_user_session(self):
        adapter = Adapter(self.client, (), FakeWorkspace(PushError('unauthorized', 'rejected', 401)))
        out = self.payload('wzstudy_catalog_list', {}, adapter)
        self.assertTrue(out['isError'])
        self.assertEqual(out['code'], 'user_session_required')
        adapter = Adapter(self.client, (), FakeWorkspace(PushError('forbidden', 'denied', 403)))
        denied = self.payload('wzstudy_roadmap_save', {'title': 'x', 'nodes': [{'title': 'x'}]}, adapter)
        self.assertEqual(denied['code'], 'workspace_denied')

    def test_user_session_credentials_are_explicit_and_origin_bound(self):
        with mock.patch.dict(os.environ, {'WZSTUDY_USER_TOKEN': '', 'WZSTUDY_BASE_URL': ''}, clear=False):
            self.assertIsNone(workspace_credentials(None))
        with mock.patch.dict(os.environ, {'WZSTUDY_USER_TOKEN': 'wzsk_source_key', 'WZSTUDY_BASE_URL': 'http://127.0.0.1:8091'}, clear=False):
            with self.assertRaises(PushError):
                workspace_credentials(None)
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'workspace.json'
            path.write_text(json.dumps({'user_token': 'wzus_abc', 'base_url': 'http://127.0.0.1:9000'}), encoding='utf-8')
            with self.assertRaises(PushError) as raised:
                workspace_credentials(str(path), 60, 'http://127.0.0.1:8091')
            self.assertEqual(raised.exception.code, 'endpoint_mismatch')
            path.write_text(json.dumps({'user_token': 'wzus_abc', 'source_key': 'wzsk_x'}), encoding='utf-8')
            with self.assertRaises(PushError) as raised:
                workspace_credentials(str(path), 60, 'http://127.0.0.1:8091')
            self.assertEqual(raised.exception.code, 'invalid_config')
            path.write_text(json.dumps({'user_token': 'wzus_abc'}), encoding='utf-8')
            client = workspace_credentials(str(path), 60, 'http://127.0.0.1:8091')
            self.assertIsNotNone(client)
            self.assertEqual(client.base_url, 'http://127.0.0.1:8091')


if __name__ == '__main__':
    unittest.main()
