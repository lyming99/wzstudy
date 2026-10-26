from pathlib import Path
import contextlib
import http.server
import io
import json
import os
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
import wzstudy_push as push

SID = '92e4de76-e7cb-49af-a4c8-05f458bd7ac6'
KEY = 'wzsk_' + 'test-only-never-a-real-secret_' * 2


class ClientTests(unittest.TestCase):
    def test_stable_url_ids_and_fragment_removed(self):
        one = push.normalize({'title':'One', 'url':'https://example.org/a#fragment'})
        two = push.normalize({'title':'Renamed', 'url':'https://example.org/a'})
        self.assertEqual(one['external_id'], two['external_id'])
        self.assertEqual(one['url'], 'https://example.org/a')
        self.assertEqual(push.normalize({'title':'No URL', 'external_id':''})['external_id'],
                         push.normalize({'title':'No URL'})['external_id'])

    def test_field_limits_types_and_kind(self):
        for value in [{'title':''}, {'title':'x','format':'html'}, {'title':'中'*200},
                      {'title':'x','content':'x'*262145}, {'title':'x','unknown':'x'},
                      {'title':'x','kind':'audio'}, {'title':'x','url':'https://name:secret@example.org'},
                      {'title':'x','published_at':'2020-01-01'}, {'title':'x','tags':['x'*81]}]:
            with self.subTest(value=list(value)):
                with self.assertRaises(push.PushError): push.normalize(value)
        tags = push.normalize({'title':'x','tags':['Go','AI','Go']})['tags']
        self.assertEqual(tags, ['AI','Go'])

    def test_batch_count_and_encoded_size(self):
        items = [push.normalize({'external_id':str(i),'title':'Item '+str(i)}) for i in range(101)]
        self.assertEqual([len(x) for x in push.batches(items)], [100,1])
        big = [push.normalize({'external_id':str(i),'title':'x','content':'中'*80000}) for i in range(40)]
        groups = push.batches(big)
        self.assertGreater(len(groups), 1)
        self.assertTrue(all(len(push.encode({'items':g})) <= push.MAX_JSON for g in groups))

    def test_duplicate_ids_and_jsonl(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp)/'input.jsonl'
            path.write_text('{"title":"a","external_id":"same"}\n{"title":"b","external_id":"same"}', encoding='utf-8')
            with self.assertRaises(push.PushError):push.load_items(path)
            path.write_text('{"title":"a","external_id":"one"}\n{"title":"b","external_id":"two"}', encoding='utf-8')
            self.assertEqual(len(push.load_items(path)), 2)

    def test_endpoint_security(self):
        self.assertEqual(push.endpoint('http://127.0.0.1:8091/api/v1/'), 'http://127.0.0.1:8091')
        self.assertEqual(push.endpoint('https://example.org/base'), 'https://example.org/base')
        for value in ['http://example.org','https://name:key@example.org','https://example.org/?token=x',
                      'https://example.org/#fragment','file:///tmp/data','https://example.org/../other']:
            with self.assertRaises(push.PushError):push.endpoint(value)

    def test_credentials_require_source_pair_and_bind_endpoint(self):
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {}, clear=True):
            path = Path(temp)/'client.json'
            path.write_text(json.dumps({'base_url':'http://127.0.0.1:8091','source_id':SID,'source_key':KEY}), encoding='utf-8')
            self.assertEqual(push.credentials(str(path))[1], SID)
            with self.assertRaises(push.PushError):push.credentials(str(path), 'https://attacker.example')
            os.environ['WZSTUDY_SOURCE_ID'] = SID
            with self.assertRaises(push.PushError):push.credentials(str(path))
            os.environ['WZSTUDY_SOURCE_KEY'] = 'administrator-key-not-a-source-key'
            with self.assertRaises(push.PushError):push.credentials(str(path))

    def test_dry_run_requires_no_secret_or_network(self):
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {}, clear=True):
            path = Path(temp)/'item.json'
            path.write_text('{"title":"A dry run"}', encoding='utf-8')
            out = io.StringIO()
            with patch.object(push.Client, 'request', side_effect=AssertionError('network call')), contextlib.redirect_stdout(out):
                self.assertEqual(push.main(['push','--input',str(path),'--dry-run']), 0)
            self.assertEqual(json.loads(out.getvalue())['network_requests'], 0)

    def test_doctor_checks_ingest_status_without_inbox_or_writes(self):
        client = push.Client('http://127.0.0.1:8091', SID, KEY)
        status = {'source_id': SID, 'enabled': True, 'permissions': ['ingest', 'upload'], 'auto_publish': False}
        with patch.object(client, 'request', side_effect=[{'status': 'ready'}, status]) as request:
            result = client.doctor()
        self.assertTrue(result['source_authenticated'])
        self.assertEqual(result['permissions'], ['ingest', 'upload'])
        self.assertFalse(result['auto_publish'])
        self.assertEqual([call.args for call in request.call_args_list],
                         [('/health/ready',), ('/ingest/' + SID + '/status',)])
        self.assertEqual(request.call_args_list[0].kwargs, {'auth': False})
        self.assertEqual(request.call_args_list[1].kwargs, {})
        self.assertNotIn(KEY, json.dumps(result))

    def test_doctor_rejects_unconfirmed_ingestion_status(self):
        client = push.Client('http://127.0.0.1:8091', SID, KEY)
        for status in [None, {}, {'source_id': SID, 'enabled': True, 'permissions': None},
                       {'source_id': SID, 'enabled': True, 'permissions': ['inbox']},
                       {'source_id': SID, 'enabled': False, 'permissions': ['ingest']},
                       {'source_id': 'other', 'enabled': True, 'permissions': ['ingest']}]:
            with self.subTest(status=status), patch.object(client, 'request', side_effect=[{'status': 'ready'}, status]):
                with self.assertRaises(push.PushError) as error:
                    client.doctor()
                self.assertEqual(error.exception.code, 'invalid_response')

    def test_upload_stream_and_validation(self):
        class Capture:
            source_id = SID
            calls = 0
            def request(self, path, method, body, content_type, length):
                self.calls += 1
                self.path, self.content_type = path, content_type
                self.asserted = body.read()
                if len(self.asserted) != length:raise AssertionError('incorrect multipart length')
                return {'created':1,'items':[{'id':'one','external_id':'file','revision':1,'status':'pending'}]}
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp)/'声音.wav'
            path.write_bytes(b'RIFF' + b'\0'*100)
            client = Capture()
            result = push.upload(client, {'title':'Audio'}, [path])
            self.assertEqual(result['created'], 1)
            self.assertEqual(client.calls, 1)
            self.assertIn(SID+'/files', client.path)
            self.assertIn(b'name="metadata"', client.asserted)
            self.assertIn(b'name="files"', client.asserted)
            with self.assertRaises(push.PushError):push.upload(client, {'title':'wrong','kind':'video'}, [path])
            html = Path(temp)/'page.html';html.write_text('<html>not allowed</html>')
            with self.assertRaises(push.PushError):push.upload(client, {'title':'wrong'}, [html])

    def test_redirect_is_not_followed(self):
        counts = []
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):pass
            def do_GET(self):
                counts.append(self.path)
                self.send_response(302)
                self.send_header('Location', '/credential-leak-target')
                self.end_headers()
        server = http.server.ThreadingHTTPServer(('127.0.0.1',0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True);thread.start()
        try:
            client = push.Client(f'http://127.0.0.1:{server.server_port}', SID, KEY)
            with self.assertRaises(push.PushError) as result:client.request('/health/ready')
            self.assertEqual(result.exception.code, 'redirect_refused')
            self.assertEqual(len(counts), 1)
        finally:
            server.shutdown();server.server_close();thread.join()

    def test_ambiguous_write_is_not_retried_or_secret_echoed(self):
        client = push.Client('http://127.0.0.1:1', SID, KEY, timeout=.2)
        with self.assertRaises(push.PushError) as result:client.request('/ingest/'+SID, 'POST', b'{}')
        self.assertTrue(result.exception.uncertain)
        self.assertNotIn(KEY, str(result.exception))


if __name__ == '__main__':
    unittest.main()
