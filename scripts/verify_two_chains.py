"""Real-service, browser and MCP regression in a disposable synthetic workspace.
Never reads live credentials or user materials. Build .artifacts/wzstudy.exe first.
"""
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError
import hashlib
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import wave

from playwright.sync_api import sync_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
ARTIFACTS.mkdir(exist_ok=True)
CHECKS, ERRORS, EXTERNAL = [], [], []
ADMIN, READ, PASSWORD = (secrets.token_hex(24) for _ in range(3))
EMAIL = 'two-chains@example.test'
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    PORT = sock.getsockname()[1]
BASE = f'http://127.0.0.1:{PORT}'
PROCESS = None
sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def api(path, method='GET', body=None, key=ADMIN, expected=200):
    headers = {'Authorization': 'Bearer ' + key} if key else {}
    if body is not None:
        headers['Content-Type'] = 'application/json'
    request = Request(BASE + '/api/v1' + path, method=method, headers=headers,
                      data=json.dumps(body, ensure_ascii=False).encode() if body is not None else None)
    try:
        with urlopen(request, timeout=30) as response:
            assert response.status == expected, (path, response.status, expected)
            return json.load(response)['data']
    except HTTPError as error:
        try:
            data = json.load(error)
            assert error.code == expected, (path, error.code, expected, data.get('error', {}).get('code'))
            return data
        finally:
            error.close()


def start(data, log, bootstrap=True):
    global PROCESS
    env = {**os.environ, 'APP_ENV': 'test', 'STORE': 'file', 'DATA_DIR': str(data),
           'HTTP_ADDR': f'127.0.0.1:{PORT}', 'ADMIN_API_KEY': ADMIN, 'READ_API_KEY': READ,
           'POLL_ENABLED': 'false', 'SYSTEM_ADMIN_EMAIL': EMAIL if bootstrap else '',
           'SYSTEM_ADMIN_PASSWORD': PASSWORD if bootstrap else '', 'BOOTSTRAP_ADMIN_EMAIL': '',
           'BOOTSTRAP_ADMIN_PASSWORD': '', 'UPLOAD_MAX_MB': '10'}
    PROCESS = subprocess.Popen([str(ARTIFACTS / 'wzstudy.exe')], cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT)
    for _ in range(100):
        if PROCESS.poll() is not None:
            raise RuntimeError('Isolated server exited; inspect two-chains-service.log')
        try:
            with urlopen(BASE + '/api/v1/health/ready', timeout=0.5) as response:
                if response.status == 200:
                    return
        except (URLError, OSError):
            time.sleep(0.1)
    raise RuntimeError('Isolated service did not become ready')


def stop():
    global PROCESS
    if PROCESS and PROCESS.poll() is None:
        PROCESS.terminate()
        try:
            PROCESS.wait(timeout=10)
        except subprocess.TimeoutExpired:
            PROCESS.kill()
            PROCESS.wait()
    PROCESS = None


def login(page):
    page.locator('#login-email').fill(EMAIL)
    page.locator('#login-password').fill(PASSWORD)
    page.locator('#login-form button[type=submit]').click()
    expect(page.locator('#login-root')).to_be_empty()
    expect(page.locator('.sidebar [data-route]')).to_have_count(2)


def nav(page, route):
    main = 'query' if route in ('query', 'clients') else 'library'
    if page.viewport_size['width'] <= 700 and 'mobile-open' not in (page.locator('body').get_attribute('class') or ''):
        page.locator('[data-action=open-nav]').click()
    page.locator(f'.sidebar [data-route="{main}"]').click()
    if route != main:
        page.locator(f'.section-tabs [data-route="{route}"]').click()
    expect(page.locator('h1')).to_have_text({'library': '数据录入', 'sources': '录入端管理', 'access': '接入指引', 'receipts': '入库记录', 'query': '数据查询', 'clients': '查询客户端'}[route])


def close_modal(page):
    page.locator('.dialog-footer [data-action=close-modal]').click()


def rpc(method, params=None, identifier=1):
    return {'jsonrpc': '2.0', 'id': identifier, 'method': method, 'params': {
        '_meta': {'io.modelcontextprotocol/protocolVersion': '2026-07-28',
                  'io.modelcontextprotocol/clientInfo': {'name': 'isolated-test', 'version': '1'},
                  'io.modelcontextprotocol/clientCapabilities': {}}, **(params or {})}}


def mcp_run(config, messages, allowed=None):
    command = [sys.executable, '-X', 'utf8', str(ROOT / 'mcp/wzstudy_ingest/server.py'), '--config', str(config)]
    if allowed:
        command += ['--allow-root', str(allowed)]
    env = dict(os.environ)
    for name in ['WZSTUDY_SOURCE_ID', 'WZSTUDY_SOURCE_KEY', 'WZSTUDY_CONFIG', 'WZSTUDY_BASE_URL']:
        env.pop(name, None)
    result = subprocess.run(command, input=''.join(json.dumps(m) + '\n' for m in messages),
                            capture_output=True, text=True, encoding='utf-8', timeout=90, cwd=ROOT, env=env)
    assert result.returncode == 0, 'MCP adapter process failed (no private config printed)'
    outputs = [json.loads(line) for line in result.stdout.splitlines()]
    key = json.loads(config.read_text(encoding='utf-8'))['source_key']
    assert key not in result.stdout and key not in result.stderr
    return outputs


def verify_query_detail_interactions(page, item_id):
    requests = []
    def observe(request):
        if '/api/v1/' in request.url:
            requests.append(request.url)
    page.on('request', observe)
    title = page.locator('.query-table .query-title')
    view = page.locator('.query-table .query-detail-button')
    ready = page.locator('[data-query-detail-state=ready]')
    endpoint = BASE + '/api/v1/items/' + item_id
    payload = {'data': api('/items/' + item_id)}
    expect(page.locator('.query-table th').last).to_have_text('操作')
    expect(view).to_have_text('查看详情')
    title.click()
    expect(ready).to_be_visible()
    expect(page.locator('.dialog .detail-content')).to_contain_text('approved body')
    expect(page.locator('.public-detail-meta')).to_contain_text(item_id)
    close_modal(page)
    expect(title).to_be_focused()
    page.locator('.query-result-row td').nth(2).click()
    expect(ready).to_be_visible()
    close_modal(page)
    view.click()
    expect(ready).to_be_visible()
    close_modal(page)
    view.focus()
    page.keyboard.press('Enter')
    expect(ready).to_be_visible()
    page.keyboard.press('Escape')
    expect(page.locator('.dialog')).to_have_count(0)
    expect(view).to_be_focused()

    held = []
    page.route(endpoint, lambda route: held.append(route))
    title.click()
    expect(page.locator('[data-query-detail-state=loading]')).to_be_visible()
    assert held
    page.keyboard.press('Escape')
    held.pop().fulfill(status=200, content_type='application/json', body=json.dumps(payload))
    page.wait_for_timeout(180)
    expect(page.locator('.dialog')).to_have_count(0)
    title.click()
    expect(page.locator('[data-query-detail-state=loading]')).to_be_visible()
    assert held
    page.evaluate("() => navigate('sources')")
    held.pop().fulfill(status=200, content_type='application/json', body=json.dumps(payload))
    page.wait_for_timeout(180)
    expect(page.locator('.dialog')).to_have_count(0)
    page.unroute(endpoint)
    nav(page, 'query')
    expect(title).to_be_visible()

    page.route(endpoint, lambda route: route.fulfill(status=503, content_type='application/json', body=json.dumps({
        'error': {'code': 'internal_error', 'message': 'temporary detail fixture'}})))
    view.click()
    expect(page.locator('[data-query-detail-state=error]')).to_contain_text('temporary detail fixture')
    page.unroute(endpoint)
    page.locator('.dialog-footer [data-action=query-detail]').click()
    expect(ready).to_be_visible()
    close_modal(page)
    assert not any('/api/v1/admin/items' in url for url in requests), 'Public query detail fell back to management data'
    page.remove_listener('request', observe)
    check('查询详情支持标题/整行/按钮/键盘，加载反馈、关闭和切页防迟到、失败重试且只调用公开接口')


def main():
    keys = []
    with tempfile.TemporaryDirectory(prefix='two-chains-', dir=ARTIFACTS) as temp, (ARTIFACTS / 'two-chains-service.log').open('wb') as log:
        data = Path(temp)
        start(data, log)
        try:
            with sync_playwright() as pw:
                browser = pw.chromium.launch(channel='chrome', headless=True)
                context = browser.new_context(viewport={'width': 1600, 'height': 1100}, locale='zh-CN', reduced_motion='reduce')
                page = context.new_page()
                page.set_default_timeout(12000)
                page.on('pageerror', lambda error: ERRORS.append(str(error)))
                page.on('request', lambda r: EXTERNAL.append(r.url) if r.url.startswith(('http:', 'https:')) and not r.url.startswith(BASE + '/') else None)
                page.goto(BASE + '/admin/#push')
                login(page)
                expect(page.locator('h1')).to_have_text('数据录入')
                expect(page.locator('.intake-card h3')).to_have_text(['采集程序', 'Skill', 'MCP'])
                expect(page.locator('.sidebar [data-route=push]')).to_have_count(0)
                check('旧推送地址回落数据录入，仅有录入/查询两个一级入口，三类录入方式准确展示')

                nav(page, 'sources')
                page.locator('.heading-actions [data-action=new-source]').click()
                page.locator('#source-name').fill('隔离采集程序')
                expect(page.locator('#source-kind')).to_have_value('manual')
                assert page.locator('#client-permissions input').count() == 2
                assert page.locator('#client-permissions input[value=read]').count() == 0
                page.locator('#source-desc').fill('<img src=x onerror="window.__two_chain_xss=1">')
                page.locator('button[form=source-form]').click()
                expect(page.locator('#one-time-key')).to_be_visible()
                source_key = page.locator('#one-time-key').input_value()
                assert source_key.startswith('wzsk_')
                keys.append(source_key)
                close_modal(page)
                source = next(s for s in api('/admin/workspace')['sources'] if s['name'] == '隔离采集程序')
                assert source['permissions'] == ['ingest', 'upload']
                api('/items', key=source_key, expected=401)
                check('界面创建独立录入端，仅能配置入库/上传；录入 Key 不可查询')

                nav(page, 'clients')
                page.locator('[data-action=new-client]').click()
                page.locator('#client-name').fill('独立学习客户端')
                page.locator('#client-desc').fill('<svg onload="window.__two_chain_xss=1">')
                assert page.locator('#client-permissions input').count() == 3
                expect(page.locator('#client-permissions input[value=feedback]')).not_to_be_checked()
                page.locator('button[form=client-form]').click()
                expect(page.locator('#one-time-key')).to_be_visible()
                query_key = page.locator('#one-time-key').input_value()
                assert query_key.startswith('wzck_') and query_key != source_key
                keys.append(query_key)
                close_modal(page)
                client = api('/admin/clients')[0]
                assert client['permissions'] == ['read', 'sync']
                api('/ingest/' + source['id'], 'POST', {'items': [{'title': 'not allowed', 'external_id': 'denied'}]}, key=query_key, expected=401)
                api('/ingest/' + source['id'] + '/files', 'POST', {}, key=query_key, expected=401)
                api('/admin/clients', key=query_key, expected=401)
                check('界面创建独立查询客户端；查询 Key 不可录入、上传或访问管理接口')

                page.locator('[data-action=edit-client]').click()
                page.evaluate('() => {state.clients=[]}')
                page.locator('button[form=client-form]').click()
                expect(page.locator('#form-error')).to_contain_text('没有创建新身份')
                assert len(api('/admin/clients')) == 1
                close_modal(page)
                page.reload()
                expect(page.locator('.client-row')).to_have_count(1)
                check('编辑对象从前端状态消失时拒绝保存，不会误发新建请求')

                held = []
                pattern = BASE + '/api/v1/admin/clients'
                def delay_client(route):
                    held.append(route) if route.request.method == 'POST' else route.continue_()
                page.route(pattern, delay_client)
                page.locator('[data-action=new-client]').click()
                page.locator('#client-name').fill('Delayed response fixture')
                page.locator('button[form=client-form]').click()
                expect(page.locator('button[form=client-form]')).to_be_disabled()
                for _ in range(40):
                    if held:
                        break
                    page.wait_for_timeout(50)
                assert len(held) == 1
                page.keyboard.press('Escape')
                expect(page.locator('#client-form')).to_be_visible()
                page.evaluate("() => navigate('query')")
                expect(page.locator('#client-form')).to_be_visible()
                page.evaluate("() => showLogin('isolated delayed response test')")
                login(page)
                held[0].fulfill(status=201, content_type='application/json', body=json.dumps({'data': {
                    'client': {'id': 'late-fixture', 'slug': 'late', 'name': 'Late response', 'permissions': ['read'], 'enabled': True, 'revision': 1},
                    'access_key': 'wzck_not_a_real_key_late_response_must_not_show'}}))
                page.wait_for_timeout(200)
                expect(page.locator('#one-time-key')).to_have_count(0)
                expect(page.locator('.dialog')).to_have_count(0)
                page.unroute(pattern)
                assert len(api('/admin/clients')) == 1
                check('提交中禁止关闭/切页，失去会话后迟到的创建响应不重新展示 Key')

                content = {'external_id': 'lesson-1', 'title': 'Published candidate', 'kind': 'article', 'content': 'approved body', 'category': '人工智能', 'tags': ['AI']}
                batch = api('/ingest/' + source['id'], 'POST', {'items': [content]}, key=source_key)
                assert batch['created'] == 1 and batch['published'] == 0 and batch['receipt_id']
                item = batch['items'][0]
                assert api('/items', key=query_key)['items'] == []
                nav(page, 'receipts')
                expect(page.locator('.receipt-table')).to_be_visible()
                expect(page.locator('#intake-results')).to_contain_text('待审核')
                page.screenshot(path=str(ARTIFACTS / 'two-chains-receipts.png'), full_page=True)
                item = api('/admin/items/' + item['id'] + '/publish', 'POST', {'revision': item['revision']})
                nav(page, 'query')
                page.locator('#query-kind').select_option('article')
                page.locator('#query-category').fill('人工智能')
                page.locator('#query-tag').fill('AI')
                page.locator('#query-q').fill('published')
                page.locator('button[form=query-form]').click()
                expect(page.locator('.query-table .material-name')).to_have_text('Published candidate')
                verify_query_detail_interactions(page, item['id'])
                check('真实回执与公开查询预览可用；类型/分类/标签/关键词筛选、公开详情及审核边界正确')

                updated = api('/ingest/' + source['id'], 'POST', {'items': [{**content, 'title': 'PRIVATE edit', 'content': 'must not leak'}]}, key=source_key)
                assert updated['updated'] == 1 and updated['items'][0]['id'] == item['id']
                assert api('/items/' + item['id'], key=query_key)['title'] == 'Published candidate'
                page.locator('button[form=query-form]').click()
                expect(page.locator('.query-table .material-name')).to_have_text('Published candidate')
                assert 'must not leak' not in page.locator('#query-results').inner_text()
                page.locator('.query-title').click()
                expect(page.locator('[data-query-detail-state=ready]')).to_be_visible()
                expect(page.locator('.dialog .detail-content')).to_contain_text('approved body')
                expect(page.locator('.public-query-detail')).not_to_contain_text('must not leak')
                close_modal(page)
                changes = api('/changes?after=0', key=query_key)
                assert len(changes['changes']) == 1
                api('/admin/items/' + item['id'] + '/unpublish', 'POST', {'revision': updated['items'][0]['revision']})
                assert api('/changes?after=' + str(changes['next_cursor']), key=query_key)['changes'][0]['operation'] == 'delete'
                api('/items/' + item['id'], key=query_key, expected=404)
                page.locator('.query-detail-button').click()
                expect(page.locator('[data-query-detail-state=error]')).to_be_visible()
                expect(page.locator('.public-query-detail')).not_to_contain_text('must not leak')
                close_modal(page)
                check('未审核编辑不进入候选与增量流，下架删除墓碑可供客户端清除缓存')

                nav(page, 'clients')
                page.locator('[data-action=client-access]').click()
                expect(page.locator('.dialog-body')).to_contain_text('GET /api/v1/changes?after=0')
                expect(page.locator('.dialog-body')).not_to_contain_text('/api/v1/ingest')
                page.locator('[data-action=rotate-client]').click()
                page.locator('#confirm-operation').click()
                expect(page.locator('#one-time-key')).to_be_visible()
                rotated = page.locator('#one-time-key').input_value()
                keys.append(rotated)
                close_modal(page)
                api('/items', key=query_key, expected=401)
                api('/items', key=rotated)
                api('/ingest/' + source['id'] + '/status', key=source_key)
                page.locator('[data-action=edit-client]').click()
                page.locator('#permission-preset').select_option('none')
                page.locator('button[form=client-form]').click()
                expect(page.locator('.dialog')).to_have_count(0)
                api('/items', key=rotated, expected=403)
                page.locator('[data-action=edit-client]').click()
                page.locator('#permission-preset').select_option('reader')
                page.locator('button[form=client-form]').click()
                expect(page.locator('.dialog')).to_have_count(0)
                check('查询 Key 的轮换与撤权真实生效，录入端凭据不受影响')

                nav(page, 'access')
                page.locator('[data-action=mcp-guide]').click()
                expect(page.locator('.dialog-body')).to_contain_text('wzstudy_ingest_items')
                expect(page.locator('.dialog-body')).to_contain_text('--allow-root')
                expect(page.locator('#one-time-key')).to_have_count(0)
                close_modal(page)
                page.screenshot(path=str(ARTIFACTS / 'two-chains-access.png'), full_page=True)
                for route in ['library', 'sources', 'receipts', 'access', 'query', 'clients']:
                    nav(page, route)
                    assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), route
                nav(page, 'query')
                page.screenshot(path=str(ARTIFACTS / 'two-chains-query.png'), full_page=True)
                for width in [1280, 1024, 768, 390]:
                    page.set_viewport_size({'width': width, 'height': 920})
                    for route in ['library', 'sources', 'receipts', 'access', 'query', 'clients']:
                        nav(page, route)
                        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), (width, route)
                nav(page, 'library')
                page.screenshot(path=str(ARTIFACTS / 'two-chains-mobile.png'), full_page=True)
                assert page.evaluate('window.__two_chain_xss') is None
                assert page.locator('.source-card img,.client-row img').count() == 0
                assert page.evaluate('Object.keys(localStorage).length+Object.keys(sessionStorage).length') == 0
                check('接入指引不读取真实 Key；六个内页在桌面/手机可用，恶意描述转义且无凭据持久缓存')

                config = data / 'mcp-config.json'
                config.write_text(json.dumps({'base_url': BASE, 'source_id': source['id'], 'source_key': source_key}), encoding='utf-8')
                media = data / 'allowed-media'
                media.mkdir()
                audio = media / 'mcp.wav'
                with wave.open(str(audio), 'wb') as stream:
                    stream.setnchannels(1)
                    stream.setsampwidth(2)
                    stream.setframerate(8000)
                    stream.writeframes(b'\0' * 3200)
                args = {'items': [{'external_id': 'mcp-lesson', 'title': 'MCP fixture', 'content': 'real MCP submission'}]}
                messages = [rpc('server/discover'), rpc('tools/list', identifier=2),
                            rpc('tools/call', {'name': 'wzstudy_ingest_items', 'arguments': args}, 3),
                            rpc('tools/call', {'name': 'wzstudy_ingest_items', 'arguments': args}, 4),
                            rpc('tools/call', {'name': 'wzstudy_upload_files', 'arguments': {'metadata': {'external_id': 'mcp-audio', 'title': 'MCP audio', 'kind': 'audio'}, 'files': [str(audio)]}}, 5)]
                outputs = mcp_run(config, messages, media)
                assert outputs[0]['result']['resultType'] == 'complete'
                assert len(outputs[1]['result']['tools']) == 3
                first, repeat, uploaded = [v['result']['structuredContent'] for v in outputs[2:]]
                assert first['created'] == 1 and repeat['unchanged'] == 1 and first['items'][0]['id'] == repeat['items'][0]['id']
                assert uploaded['created'] == 1 and uploaded['published'] == 0 and uploaded['receipt_id']
                uploaded_item = api('/admin/items/' + uploaded['items'][0]['id'])
                assert len(uploaded_item['draft']['assets']) == 1
                assert (data / 'uploads' / uploaded_item['draft']['assets'][0]['id']).read_bytes() == audio.read_bytes()
                legacy = mcp_run(config, [
                    {'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {'protocolVersion': '2025-11-25', 'capabilities': {}, 'clientInfo': {'name': 'legacy', 'version': '1'}}},
                    {'jsonrpc': '2.0', 'method': 'notifications/initialized'},
                    {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/call', 'params': {'name': 'wzstudy_ingest_status', 'arguments': {}}}])
                assert legacy[1]['result']['structuredContent']['source_authenticated']
                check('MCP stdio 现代/旧版调用、真实 JSON 幂等入库及实际音频文件上传全部走统一 API')

                # Only manipulate the disposable fixture while its service is stopped.
                stop()
                file = data / 'workspace.json'
                snapshot = json.loads(file.read_text(encoding='utf-8'))
                clock_before = snapshot['cursor']
                snapshot['sources'][source['id']]['source']['permissions'] = ['read', 'sync', 'ingest', 'upload']
                file.write_text(json.dumps(snapshot, ensure_ascii=False), encoding='utf-8')
                start(data, log, bootstrap=False)
                preview = api('/admin/identity-migration')
                assert preview['pending'] == 1 and preview['mixed'] == 1
                api('/admin/identity-migration', 'POST', {'choices': []}, expected=409)
                api('/admin/sources/' + source['id'], 'PUT', {'slug': source['slug'], 'name': source['name'], 'kind': 'manual', 'permissions': ['ingest', 'upload'], 'revision': source['revision']}, expected=409)
                page.set_viewport_size({'width': 1600, 'height': 1100})
                page.goto(BASE + '/admin/?migration_restart=1#library')
                login(page)
                page.locator('[data-action=identity-migration]').click()
                expect(page.locator('.migration-entry')).to_have_count(1)
                choice = page.locator('[data-migration-id]')
                expect(choice).to_have_value('')
                assert not choice.evaluate('el=>el.checkValidity()')
                choice.select_option('ingest')
                page.locator('#migration-confirm').check()
                page.locator('button[form=migration-form]').click()
                expect(page.locator('#dialog-title')).to_have_text('身份拆分已提交')
                migration_key = page.locator('.migration-key').input_value()
                keys.append(migration_key)
                assert migration_key.startswith('wzck_')
                close_modal(page)
                assert api('/admin/identity-migration')['pending'] == 0
                api('/items', key=migration_key)
                api('/items', key=source_key, expected=401)
                api('/ingest/' + source['id'] + '/status', key=source_key)
                assert json.loads(file.read_text(encoding='utf-8'))['cursor'] == clock_before
                check('旧混合身份预检不自动迁移，必须明确 Key 归属；迁移后身份隔离且资料游标不变')

                stop()
                start(data, log, bootstrap=False)
                api('/items', key=rotated)
                api('/items', key=migration_key)
                api('/ingest/' + source['id'] + '/status', key=source_key)
                workspace = api('/admin/workspace')
                assert len(workspace['clients']) == 2 and 'collections' not in workspace and 'plans' not in workspace
                stored = file.read_text(encoding='utf-8')
                response = json.dumps(workspace)
                assert all(k not in stored and k not in response for k in keys)
                for path, method in [('/admin/plans', 'POST'), ('/admin/collections', 'POST'), ('/stream', 'GET'), ('/clients/old/inbox', 'GET')]:
                    api(path, method, {} if method == 'POST' else None, expected=404)
                assert not ERRORS, ERRORS
                assert not EXTERNAL, EXTERNAL
                check('重启后查询/录入 Key 与资料持久恢复；旧独立业务接口退役，无明文 Key、浏览器异常或外站请求')
                context.close()
                browser.close()
        finally:
            stop()
    report = {'result': 'passed', 'groups': len(CHECKS), 'checks': CHECKS, 'browser_errors': ERRORS,
              'external_requests': EXTERNAL, 'store': 'isolated synthetic file workspace',
              'postgres_runtime': 'not run', 'binary_sha256': hashlib.sha256((ARTIFACTS / 'wzstudy.exe').read_bytes()).hexdigest()}
    (ARTIFACTS / 'two-chains-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)


if __name__ == '__main__':
    main()
