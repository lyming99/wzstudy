import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock
import uuid

from client_connection import ConnectionConfig, ConnectionError
from feedback_client import FeedbackClient, FeedbackOutbox, make_event


def config(identifier=None):
    return ConnectionConfig('https://study.example.test', identifier or str(uuid.uuid4()), 'learner', 'wzck_' + 'x' * 43)


class FeedbackClientTests(unittest.TestCase):
    def test_action_builder_and_undo(self):
        identifier = str(uuid.uuid4())
        event = make_event(identifier, 'record', note='学习记录', progress=0.5)
        self.assertEqual(event['item_id'], identifier)
        self.assertEqual(event['note'], '学习记录')
        self.assertFalse(make_event(identifier, 'like', value=False)['value'])
        for action, kwargs in [('bad', {}), ('recommended', {'value': False}), ('like', {'note': 'not a note'}), ('record', {'progress': float('nan')}), ('record', {'progress': True})]:
            with self.assertRaises(ConnectionError):
                make_event(identifier, action, **kwargs)

    def test_candidate_filters_and_ack_validation(self):
        client = FeedbackClient(config())
        client._request = Mock(return_value={'items': []})
        client.candidates(exclude=('recommended', 'read'), category='学习方法')
        path = client._request.call_args.args[0]
        self.assertIn('exclude=recommended%2Cread', path)
        self.assertIn('include_feedback=true', path)
        event = make_event(str(uuid.uuid4()), 'read')
        client._request.return_value = {'events': [{'event_id': event['event_id'], 'sequence': 1, 'duplicate': False}]}
        self.assertEqual(client.report([event])['events'][0]['sequence'], 1)
        client._request.return_value = {'events': []}
        with self.assertRaises(ConnectionError):
            client.report([event])
        with self.assertRaises(ConnectionError):
            client.candidates(client_id=str(uuid.uuid4()))

    def test_outbox_persists_original_event_on_timeout_and_reopen(self):
        cfg = config()
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'private-feedback.sqlite'
            outbox = FeedbackOutbox(path, cfg)
            event = make_event(str(uuid.uuid4()), 'recommended')
            outbox.enqueue(event)
            outbox.enqueue(event)
            self.assertEqual(outbox.pending(), 1)
            client = FeedbackClient(cfg)
            client.report = Mock(side_effect=ConnectionError('timeout'))
            with self.assertRaises(ConnectionError):
                outbox.flush(client)
            self.assertEqual(outbox.pending(), 1)
            outbox.close()
            outbox = FeedbackOutbox(path, cfg)
            client.report = Mock(return_value={'events': [{'event_id': event['event_id'], 'sequence': 1, 'duplicate': True}]})
            outbox.flush(client)
            self.assertEqual(client.report.call_args.args[0][0], event)
            self.assertEqual(outbox.pending(), 0)
            outbox.close()
            self.assertNotIn(cfg.access_key.encode(), path.read_bytes())

    def test_outbox_scope_and_event_conflict(self):
        cfg = config()
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / 'private-feedback.sqlite'
            outbox = FeedbackOutbox(path, cfg)
            event = make_event(str(uuid.uuid4()), 'favorite')
            outbox.enqueue(event)
            changed = dict(event, value=False)
            with self.assertRaises(ConnectionError):
                outbox.enqueue(changed)
            with self.assertRaises(ConnectionError):
                outbox.flush(FeedbackClient(config()))
            outbox.close()
            with self.assertRaises(ConnectionError):
                FeedbackOutbox(path, config())

    def test_explicit_http_consent(self):
        cfg = ConnectionConfig('http://127.0.0.1:8091', str(uuid.uuid4()), 'local', 'wzck_' + 'x' * 43)
        with self.assertRaises(ConnectionError):
            FeedbackClient(cfg)
        FeedbackClient(cfg, allow_insecure_http=True)


if __name__ == '__main__':
    unittest.main()
