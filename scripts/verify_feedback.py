"""Disposable end-to-end feedback, exclusion and client GUI regression."""
import hashlib
import json
from pathlib import Path
import sys
import tempfile
from datetime import datetime, timedelta, timezone
from unittest.mock import patch

from playwright.sync_api import sync_playwright, expect
import verify_two_chains as fixture
sys.path.insert(0, str(fixture.ROOT / 'examples'))
from client_connection import ConnectionConfig, ConnectionError, verify_connection
from feedback_client import FeedbackClient, FeedbackOutbox, make_event

CHECKS = []


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def create_item(title):
    item = fixture.api('/admin/items', 'POST', {'status': 'pending', 'content': {'kind': 'article', 'title': title, 'content': 'approved feedback fixture'}}, expected=201)
    return fixture.api('/admin/items/' + item['id'] + '/publish', 'POST', {'revision': item['revision']})


def main():
    errors, external = [], []
    with tempfile.TemporaryDirectory(prefix='feedback-', dir=fixture.ARTIFACTS) as temp, (fixture.ARTIFACTS / 'feedback-service.log').open('wb') as log:
        fixture.start(Path(temp), log)
        try:
            items = [create_item('feedback article ' + str(i)) for i in range(3)]
            before = fixture.api('/changes?after=0', key=fixture.READ)
            with sync_playwright() as pw:
                browser = pw.chromium.launch(channel='chrome', headless=True)
                context = browser.new_context(viewport={'width': 1440, 'height': 1050}, locale='zh-CN', reduced_motion='reduce')
                page = context.new_page()
                page.on('pageerror', lambda error: errors.append(str(error)))
                page.on('request', lambda request: external.append(request.url) if request.url.startswith(('http://', 'https://')) and not request.url.startswith(fixture.BASE + '/') else None)
                page.goto(fixture.BASE + '/admin/#clients')
                fixture.login(page)
                page.locator('[data-action=new-client]').click()
                expect(page.locator('#client-permissions input[value=feedback]')).not_to_be_checked()
                page.locator('#client-name').fill('反馈学习端')
                page.locator('button[form=client-form]').click()
                key = page.locator('#one-time-key').input_value()
                client = fixture.api('/admin/clients')[0]
                fixture.close_modal(page)
                event = make_event(items[0]['id'], 'recommended', item_revision=items[0]['revision'])
                fixture.api('/client/feedback', 'POST', {'events': [event]}, key=key, expected=403)
                page.locator(f'[data-action=edit-client][data-id="{client["id"]}"]').click()
                page.locator('#client-permissions input[value=feedback]').check()
                page.locator('button[form=client-form]').click()
                expect(page.locator('.dialog')).to_have_count(0)
                client = fixture.api('/admin/clients/' + client['id'])
                assert client['permissions'] == ['read', 'sync', 'feedback']
                config = ConnectionConfig(fixture.BASE, client['id'], client['name'], key)
                assert verify_connection(config, allow_insecure_http=True)['permissions'] == ['read', 'sync', 'feedback']
                api = FeedbackClient(config, allow_insecure_http=True)
                check('旧默认客户端不自动获得反馈权限，界面显式授权后同一Key生效，扫码校验兼容三种能力')

                outbox = FeedbackOutbox(Path(temp) / 'private-outbox.sqlite', config)
                outbox.enqueue(event)
                with patch.object(api, '_request', side_effect=ConnectionError('simulated offline')):
                    try:
                        outbox.flush(api)
                        raise AssertionError('Offline flush unexpectedly succeeded')
                    except ConnectionError:
                        pass
                assert outbox.pending() == 1
                ack = outbox.flush(api)
                assert ack['accepted'] == 1 and outbox.pending() == 0
                assert api.report([event])['duplicates'] == 1
                check('离线队列保留原事件ID，真实上报成功后删除，重复上报不计数')

                batch = [make_event(items[0]['id'], 'impression'), make_event(items[0]['id'], 'read'),
                         make_event(items[1]['id'], 'like'), make_event(items[1]['id'], 'favorite'),
                         make_event(items[1]['id'], 'record', note='<script>window.feedback_xss=1</script>学习笔记', progress=0.75)]
                assert api.report(batch)['accepted'] == 5
                state = api.states()
                assert state['total'] == 2 and state['high_watermark'] == 6 and state['summary']['recommended'] == 1 and state['summary']['favorited'] == 1
                assert len(api.events()['events']) == 6
                assert [item['id'] for item in api.candidates(exclude=('seen',))['items']] == [items[2]['id']]
                assert len(api.candidates(exclude=('recommended', 'read'))['items']) == 2
                other = fixture.api('/admin/clients', 'POST', {'slug': 'feedback-other', 'name': '另一学习端'}, expected=201)
                assert len(fixture.api('/items?unseen=true', key=other['access_key'])['items']) == 3
                check('六类反馈有真实状态与独立游标，候选去重在分页前生效且跨客户端不串数据')

                # Independent time window lets the client choose its recommendation cooldown.
                future = (datetime.now(timezone.utc) + timedelta(seconds=2)).isoformat()
                assert len(api.candidates(exclude=('seen',), exclude_since=future)['items']) == 3
                older = make_event(items[1]['id'], 'like', value=True, occurred_at=(datetime.now(timezone.utc) - timedelta(days=1)).isoformat())
                undo = make_event(items[1]['id'], 'like', value=False)
                api.report([undo, older])
                one = api.states(item_ids=(items[1]['id'],))['items'][0]
                assert one['actions']['like']['active'] is False
                assert one['actions']['like']['count'] == 2
                check('支持推荐冷却时间和取消状态，迟到旧行为不覆盖新的取消动作')

                # View feedback via the management UI; it must never post events itself.
                page.reload()
                page.locator(f'[data-action=client-feedback][data-id="{client["id"]}"]').click()
                expect(page.locator('.client-feedback-dialog')).to_be_visible()
                expect(page.locator('.feedback-note')).to_contain_text('<script>window.feedback_xss=1</script>')
                assert page.evaluate('window.feedback_xss') is None
                assert page.locator('.feedback-note script').count() == 0
                expect(page.locator('.client-feedback-dialog')).to_contain_text('学习进度 75%')
                expect(page.locator('.toast')).to_have_count(0, timeout=7000)
                page.screenshot(path=str(fixture.ARTIFACTS / 'feedback-desktop.png'), full_page=True, animations='disabled')
                page.set_viewport_size({'width': 390, 'height': 920})
                assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
                page.screenshot(path=str(fixture.ARTIFACTS / 'feedback-mobile.png'), full_page=True, animations='disabled')
                fixture.close_modal(page)
                page.set_viewport_size({'width': 1440, 'height': 1050})
                fixture.nav(page, 'query')
                before_preview = api.events()['high_watermark']
                page.locator('#query-client').select_option(client['id'])
                page.locator('input[name=exclude][value=seen]').check()
                page.locator('button[form=query-form]').click()
                expect(page.locator('.query-title')).to_have_count(1)
                expect(page.locator('.query-title')).to_have_text(items[2]['draft']['title'])
                assert api.events()['high_watermark'] == before_preview
                page.locator('#query-client').select_option(other['client']['id'])
                page.locator('button[form=query-form]').click()
                expect(page.locator('.query-title')).to_have_count(3)
                check('反馈记录含安全转义笔记和进度，桌面/手机可用；管理端按身份预览但不伪造已读或推荐')

                # Undo current read/favorite/record; recommended and seen remain historical facts.
                api.report([make_event(items[0]['id'], 'read', value=False), make_event(items[1]['id'], 'favorite', value=False), make_event(items[1]['id'], 'record', value=False)])
                states = api.states()
                assert states['summary'].get('read', 0) == 0 and states['summary'].get('favorited', 0) == 0
                assert len(api.candidates(exclude=('read', 'favorited', 'recorded'))['items']) == 3
                assert len(api.candidates(exclude=('seen',))['items']) == 1
                assert api.states(item_ids=(items[1]['id'],))['items'][0]['actions']['record'].get('note', '') == ''
                after = fixture.api('/changes?after=0', key=fixture.READ)
                assert before['high_watermark'] == after['high_watermark'] and len(before['changes']) == len(after['changes'])
                check('已读/收藏/记录可撤销而历史曝光不丢失，反馈完全不污染资料变更游标')

                # Restart only the disposable service, preserving all event and source identities.
                fixture.stop()
                fixture.start(Path(temp), log, bootstrap=False)
                assert api.events()['high_watermark'] == 11
                assert len(api.candidates(exclude=('seen',))['items']) == 1
                assert api.report([event])['duplicates'] == 1
                assert api.events()['high_watermark'] == 11
                outbox.close()
                assert key.encode() not in (Path(temp) / 'private-outbox.sqlite').read_bytes()
                assert not errors and not external
                check('服务重启后状态/计数/去重恢复，旧事件仍幂等，队列不存Key且无浏览器异常或外站请求')
                context.close()
                browser.close()
                report = {'result': 'passed', 'groups': len(CHECKS), 'checks': CHECKS,
                          'browser_errors': errors, 'external_requests': external,
                          'store': 'disposable synthetic workspace; no real materials or credentials',
                          'binary_sha256': hashlib.sha256((fixture.ARTIFACTS / 'wzstudy.exe').read_bytes()).hexdigest()}
                (fixture.ARTIFACTS / 'feedback-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
                print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
        finally:
            fixture.stop()


if __name__ == '__main__':
    main()
