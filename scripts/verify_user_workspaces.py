"""Isolated real-service + browser verification of user-owned ingestion/learning.
Never reads .local, production credentials or the user's running service.
"""
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.parse import urlsplit
from urllib.error import HTTPError, URLError
import json
from datetime import datetime, timezone
import os
import secrets
import socket
import struct
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.artifacts' / 'user-workspaces'
BINARY = ROOT / '.artifacts' / ('wzstudy-users.exe' if os.name == 'nt' else 'wzstudy-users')
OUT.mkdir(parents=True, exist_ok=True)
if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8')
CHECKS = []
ADMIN = secrets.token_urlsafe(32)
READER = secrets.token_urlsafe(32)
ADMIN_EMAIL = 'administrator@workspace.test'
ADMIN_PASSWORD = secrets.token_urlsafe(24)
A_EMAIL, B_EMAIL = 'alice@workspace.test', 'bob@workspace.test'
A_PASSWORD, B_PASSWORD = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    PORT = sock.getsockname()[1]
BASE = f'http://127.0.0.1:{PORT}'
process = None


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def call(path, method='GET', body=None, token=None, expected=200, raw=None, content_type=None):
    headers = {'Authorization': 'Bearer ' + token} if token else {}
    if body is not None:
        raw = json.dumps(body).encode('utf-8')
        headers['Content-Type'] = 'application/json'
    if content_type:
        headers['Content-Type'] = content_type
    req = Request(BASE + '/api/v1' + path, method=method, headers=headers, data=raw)
    try:
        response = urlopen(req, timeout=15)
    except HTTPError as error:
        response = error
    with response:
        status, data = response.status, response.read()
    assert status == expected, f'{method} {path}: {status}, expected {expected}'
    if not data or not data.startswith(b'{'):
        return data
    result = json.loads(data)
    return result.get('data', result)


def login(email, password):
    return call('/user/auth/login', 'POST', {'email': email, 'password': password, 'session_mode': 'token'})['access_token']


def stop():
    global process
    if process and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
    process = None


def start(env, log):
    global process
    process = subprocess.Popen([str(BINARY)], cwd=ROOT, env=env, stdout=log, stderr=log)
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError('isolated service exited before readiness; inspect isolated log')
        try:
            call('/health/ready')
            return
        except (URLError, OSError, AssertionError):
            time.sleep(.1)
    raise RuntimeError('isolated service readiness timeout')


def browser_login(page, route, email, password, admin=False):
    page.goto(BASE + route)
    page.get_by_label('用户邮箱' if route == '/learn/' else '登录邮箱').fill(email)
    page.get_by_label('登录密码').fill(password)
    page.get_by_role('button', name='登录' if admin else ('登录工作空间' if route == '/workspace/' else '进入个人学习'), exact=True).click()
    if not admin and route == '/workspace/':
        page.locator('.sidebar [data-route="library"]').click()


def main():
    (OUT / "verification.json").unlink(missing_ok=True)
    subprocess.run(['go', 'build', '-o', str(BINARY), './cmd/api'], cwd=ROOT, check=True)
    with tempfile.TemporaryDirectory(prefix='workspace-verify-', dir=OUT) as tmp:
        directory = Path(tmp)
        env = dict(os.environ)
        for name in list(env):
            if name.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):
                env.pop(name)
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(directory / 'data'),
                   HTTP_ADDR=f'127.0.0.1:{PORT}', ADMIN_API_KEY=ADMIN, READ_API_KEY=READER,
                   SYSTEM_ADMIN_EMAIL=ADMIN_EMAIL, SYSTEM_ADMIN_PASSWORD=ADMIN_PASSWORD,
                   POLL_ENABLED='false', CORS_ORIGINS='', UPLOAD_MAX_MB='10')
        with (directory / 'service.log').open('ab') as log:
            try:
                start(env, log)
                a = call('/admin/users', 'POST', {'email': A_EMAIL, 'display_name': '用户 Alice', 'password': A_PASSWORD}, ADMIN, 201)
                b = call('/admin/users', 'POST', {'email': B_EMAIL, 'display_name': '用户 Bob', 'password': B_PASSWORD}, ADMIN, 201)
                a1, a2, bt = login(A_EMAIL, A_PASSWORD), login(A_EMAIL, A_PASSWORD), login(B_EMAIL, B_PASSWORD)
                for key in [ADMIN, READER, None]:
                    call('/workspace/workspace', token=key, expected=401)
                    call('/items', token=key, expected=401)
                    call('/client/learning', token=key, expected=401)
                call('/admin/users', token=a1, expected=401)
                call('/admin/items', 'POST', {}, ADMIN, 404)
                check('管理员仅管理用户；用户、管理员和全局读取凭据不混权')

                source_body = {'slug': 'same-source', 'name': '个人来源', 'permissions': ['ingest', 'upload']}
                sa = call('/workspace/sources', 'POST', source_body, a1, 201)
                sb = call('/workspace/sources', 'POST', source_body, bt, 201)
                assert sa['source']['owner_id'] == a['id'] and sb['source']['owner_id'] == b['id']
                def ingest(source, title):
                    return call('/ingest/' + source['source']['id'], 'POST', {'items': [{'external_id': 'same-external', 'title': title, 'content': '只属于当前账号的学习正文。', 'kind': 'article'}]}, source['access_key'])['items'][0]
                ia, ib = ingest(sa, 'Alice 的个人资料'), ingest(sb, 'Bob 的个人资料')
                qa = call('/workspace/clients', 'POST', {'slug': 'same-query', 'name': '个人查询', 'permissions': ['read', 'sync']}, a1, 201)
                qb = call('/workspace/clients', 'POST', {'slug': 'same-query', 'name': '个人查询', 'permissions': ['read', 'sync']}, bt, 201)
                for token, own, foreign in [(a1, ia, ib), (bt, ib, ia), (qa['access_key'], ia, ib), (qb['access_key'], ib, ia)]:
                    listing = call('/items', token=token)
                    assert [i['id'] for i in listing['items']] == [own['id']]
                    call('/items/' + foreign['id'], token=token, expected=404)
                    changes = call('/changes', token=token)
                    assert changes['high_watermark'] == 1 and len(changes['changes']) == 1
                call('/workspace/sources/' + sa['source']['id'], token=bt, expected=404)
                call('/ingest/' + sa['source']['id'], 'POST', {'items': []}, sb['access_key'], 401)
                call('/client/learning', token=qa['access_key'], expected=401)
                check('相同来源/客户端 slug 与 external_id 可并存，资料、查询及游标按用户隔离')

                boundary = 'verify-' + secrets.token_hex(10)
                metadata = {'status': 'pending', 'content': {'kind': 'audio', 'title': 'Alice 私人音频'}}
                wav = b'RIFF' + struct.pack('<I', 136) + b'WAVEfmt ' + struct.pack('<IHHIIHH', 16, 1, 1, 8000, 16000, 2, 16) + b'data' + struct.pack('<I', 100) + bytes(100)
                raw = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n'.encode() + json.dumps(metadata).encode() +
                       f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="files"; filename="private.wav"\r\nContent-Type: audio/wav\r\n\r\n'.encode() + wav + f'\r\n--{boundary}--\r\n'.encode())
                audio = call('/workspace/uploads', 'POST', token=a1, expected=201, raw=raw, content_type='multipart/form-data; boundary=' + boundary)
                asset = audio['draft']['assets'][0]
                call(asset['url'].removeprefix('/api/v1'), token=a1)
                call(asset['url'].removeprefix('/api/v1'), token=bt, expected=404)
                call(asset['url'].removeprefix('/api/v1'), token=ADMIN, expected=401)
                call('/workspace/items', 'POST', {'status': 'pending', 'content': {'kind': 'audio', 'title': 'forged', 'assets': [asset]}}, bt, 400)
                check('附件、草稿媒体与跨用户附件引用均无越权')

                record_id, event_id, draft_event, collection_id = [str(uuid.uuid4()) for _ in range(4)]
                events = [{'event_id': event_id, 'item_id': ia['id'], 'action': 'record_create', 'record_id': record_id, 'text': 'Alice API 私人笔记'}]
                call('/client/learning/events', 'POST', {'events': events}, a1)
                call('/client/learning/events', 'POST', {'events': events}, bt, 404)
                events[0]['item_id'] = ib['id']
                call('/client/learning/events', 'POST', {'events': events}, bt)
                call('/client/learning/drafts/commands', 'POST', {'event_id': draft_event, 'item_id': ia['id'], 'text': 'Alice 私人草稿', 'expected_revision': 0}, a1)
                call('/client/learning/drafts/events/' + draft_event, token=bt, expected=404)
                call('/client/learning/collections/commands', 'POST', {'event_id': str(uuid.uuid4()), 'collection_id': collection_id, 'action': 'create', 'title': 'Alice 个人合集', 'expected_revision': 0}, a1)
                call('/client/learning/collections/' + collection_id, token=bt, expected=404)
                call('/client/learning/items/' + ia['id'] + '/records/' + record_id, token=bt, expected=404)
                own = call('/client/learning/items/' + ia['id'] + '/records', token=a2)
                assert own['total'] == 1
                call('/user/auth/logout', 'POST', token=a1)
                call('/client/learning', token=a1, expected=401)
                call('/client/learning', token=a2)
                check('学习记录/草稿/合集按用户隔离；同账号跨设备同步且退出仅撤销当前会话')

                from playwright.sync_api import sync_playwright
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    errors = []
                    admin_context = browser.new_context(viewport={'width': 1440, 'height': 1000})
                    admin_page = admin_context.new_page()
                    admin_page.on('pageerror', lambda error: errors.append(str(error)))
                    browser_login(admin_page, '/admin/', ADMIN_EMAIL, ADMIN_PASSWORD, True)
                    admin_page.get_by_text(A_EMAIL, exact=True).wait_for()
                    admin_page.screenshot(path=str(OUT / 'admin-users.png'), full_page=True)
                    held_admin = []
                    admin_page.route('**/api/v1/admin/users/' + a['id'], lambda route: held_admin.append(route))
                    admin_page.locator('.collection-row').filter(has_text=A_EMAIL).get_by_role('button', name='编辑 / 重置密码', exact=True).click()
                    admin_page.locator('#user-name').fill('保存中的 Alice')
                    admin_page.get_by_role('button', name='保存账号', exact=True).click()
                    admin_page.wait_for_function("() => document.querySelector('#user-form')?.dataset.busy === 'true'")
                    admin_page.get_by_role('button', name='创建用户', exact=True).click()
                    assert admin_page.locator('#user-form').get_attribute('data-id') == a['id']
                    assert held_admin, 'admin save interception missing'
                    held_admin.pop().fulfill(status=200, content_type='application/json', body=json.dumps({'data': a}))
                    admin_page.locator('#form-panel').wait_for(state='hidden')
                    admin_page.unroute('**/api/v1/admin/users/' + a['id'])
                    check('管理员保存账号时禁止切换编辑对象，避免旧响应隐藏新表单')

                    assert admin_page.locator('[data-route="library"]').count() == 0
                    context = browser.new_context(viewport={'width': 1440, 'height': 1000})
                    page = context.new_page()
                    page.on('pageerror', lambda error: errors.append(str(error)))
                    browser_login(page, '/workspace/', A_EMAIL, A_PASSWORD)
                    page.locator('[data-action="upload"]').first.wait_for()
                    assert page.get_by_text('Bob 的个人资料', exact=True).count() == 0
                    page.locator('[data-action="upload"]').first.click()
                    page.locator('#material-title').fill('Alice UI 录入资料')
                    page.locator('#article-content').fill('这是一篇由当前用户在真实浏览器录入的学习文章。')
                    page.locator('button[form="upload-form"]').click()
                    page.locator('#modal-root').wait_for(state='hidden')
                    own_page = call('/workspace/items?q=Alice%20UI', token=a2)
                    assert own_page['total'] == 1
                    ui_item = own_page['items'][0]
                    call('/workspace/items/' + ui_item['id'] + '/publish', 'POST', {'revision': ui_item['revision']}, a2)
                    page.screenshot(path=str(OUT / 'user-workspace.png'), full_page=True)
                    page.locator('.sidebar [data-route="query"]').click()
                    page.locator('[data-query-item="' + ui_item['id'] + '"]').wait_for()
                    assert page.get_by_text('Bob 的个人资料', exact=True).count() == 0
                    assert page.evaluate('queryResult.client_id') == a['id']
                    page.locator('.sidebar [data-route="clients"]').click()
                    page.get_by_role('button', name='Key / 接口说明', exact=True).first.click()
                    page.get_by_text('此 Key 只读取所属用户工作区的已发布资料', exact=False).wait_for()
                    assert '经 feedback 授权可读写' not in page.locator('.dialog').inner_text()
                    page.locator('[data-action="close-modal"]').first.click()
                    check('工作台查询使用当前用户反馈空间，客户端说明不再把 Key 视为学习身份')

                    check('真实浏览器用户录入与个人工作台闭环，管理员页面不提供资料入口')

                    roadmap_calls = []
                    def track_roadmaps(response):
                        path = urlsplit(response.url).path
                        if '/roadmaps' in path or '/api/v1/workspace/items' in path or '/api/v1/admin/items' in path:
                            roadmap_calls.append((response.request.method, path, response.status))
                    page.on('response', track_roadmaps)
                    page.locator('.sidebar [data-route="roadmaps"]').click()
                    page.get_by_role('button', name='新建路线图', exact=True).wait_for()
                    assert page.locator('#login-form').count() == 0
                    page.get_by_role('button', name='新建路线图', exact=True).click()
                    page.locator('#roadmap-title').fill('路线图登录保持回归')
                    page.locator('[data-action="roadmap-link"][data-value="' + ui_item['id'] + '"]').wait_for()
                    page.locator('[data-action="roadmap-link"][data-value="' + ui_item['id'] + '"]').click()
                    page.locator('[data-action="roadmap-save"]').click()
                    page.locator('#roadmap-save-state').filter(has_text='版本 1').wait_for()
                    roadmap_id = page.evaluate('() => roadmapEditor.doc.id')
                    document = call('/workspace/roadmaps/' + roadmap_id, token=a2)
                    assert any(ui_item['id'] in node['item_ids'] for node in document['nodes'])
                    call('/workspace/roadmaps/' + roadmap_id, token=bt, expected=404)
                    call('/workspace/roadmaps', token=ADMIN, expected=401)
                    foreign = {'revision': 1, 'title': '不允许的跨用户修改', 'summary': '', 'nodes': [{'id': str(uuid.uuid4()), 'parent_id': '', 'title': 'root', 'note': '', 'item_ids': [], 'collapsed': False}]}
                    call('/workspace/roadmaps/' + roadmap_id, 'PUT', foreign, bt, 404)
                    call('/workspace/roadmaps/' + roadmap_id + '/delete', 'POST', {'revision': 1}, bt, 404)
                    page.locator('[data-action="roadmap-back"]').click()
                    page.locator('[data-action="roadmap-edit"][data-id="' + roadmap_id + '"]').first.click()
                    page.locator('#roadmap-title').fill('路线图登录保持回归 - 已修改')
                    page.locator('[data-action="roadmap-save"]').click()
                    page.locator('#roadmap-save-state').filter(has_text='版本 2').wait_for()
                    page.locator('[data-action="roadmap-back"]').click()
                    page.locator('#roadmap-search').fill('路线图登录保持回归')
                    page.locator('#roadmap-search-form').get_by_role('button', name='搜索', exact=True).click()
                    delete_button = page.locator('[data-action="roadmap-delete"][data-id="' + roadmap_id + '"]')
                    delete_button.wait_for()
                    page.once('dialog', lambda dialog: dialog.accept())
                    delete_button.click()
                    page.locator('#roadmap-results .empty').wait_for()
                    assert page.request.get(BASE + '/api/v1/user/auth/session').status == 200
                    assert page.locator('#login-form').count() == 0
                    assert not any(status == 401 or '/api/v1/admin/' in path for method, path, status in roadmap_calls), roadmap_calls
                    expected_calls = [('GET', '/api/v1/workspace/roadmaps'), ('POST', '/api/v1/workspace/roadmaps'), ('GET', '/api/v1/workspace/roadmaps/' + roadmap_id), ('PUT', '/api/v1/workspace/roadmaps/' + roadmap_id), ('POST', '/api/v1/workspace/roadmaps/' + roadmap_id + '/delete'), ('GET', '/api/v1/workspace/items'), ('GET', '/api/v1/workspace/items/' + ui_item['id'])]
                    for method, path in expected_calls:
                        assert (method, path, 200 if method != 'POST' or path.endswith('/delete') else 201) in roadmap_calls, (method, path, roadmap_calls)
                    page.remove_listener('response', track_roadmaps)
                    check('路线图列表、新建、打开、关联资料、修改和删除均使用用户接口且保持登录，跨用户操作被拒绝')

                    page.goto(BASE + '/learn/')
                    page.locator('.lesson').filter(has_text='Alice UI 录入资料').get_by_role('button', name='开始学习 →').click()
                    page.locator('#note').wait_for()
                    page.locator('#note').fill('只属于 Alice 的 UI 笔记')
                    page.locator('[data-action="draft"]').click()
                    page.locator('#draft-state').filter(has_text='草稿已保存').wait_for()
                    page.locator('[data-action="note"]').click()
                    page.locator('#records').get_by_text('只属于 Alice 的 UI 笔记', exact=True).wait_for()
                    page.locator('[data-action="favorite"]').click()
                    page.locator('[data-action="favorite"]').filter(has_text='已收藏').wait_for()
                    page.locator('#progress-position').fill('2')
                    page.locator('[data-action="progress"]').click()
                    page.get_by_text('上次已保存，可跨设备继续。', exact=True).wait_for()
                    page.screenshot(path=str(OUT / 'user-learning.png'), full_page=True)
                    check('学习端真实保存笔记、草稿、收藏与阅读进度')

                    second = context.new_page()
                    response = second.request.post(BASE + '/api/v1/user/auth/login', data={'email': B_EMAIL, 'password': B_PASSWORD})
                    assert response.status == 200
                    second.goto(BASE + '/learn/')
                    second.get_by_role('heading', name='Bob 的个人资料', exact=True).wait_for()
                    assert second.get_by_text('Alice UI 录入资料', exact=True).count() == 0
                    page.locator('[data-action="favorite"]').click()
                    page.locator('#login').wait_for()
                    assert page.locator('#records').count() == 0
                    check('跨标签切换用户后旧页面写入被拒绝，清除上一账号的私人界面')

                    mobile = browser.new_context(viewport={'width': 390, 'height': 844}, is_mobile=True, has_touch=True)
                    mp = mobile.new_page()
                    mp.on('pageerror', lambda error: errors.append(str(error)))
                    browser_login(mp, '/learn/', B_EMAIL, B_PASSWORD)
                    mp.get_by_role('heading', name='Bob 的个人资料', exact=True).wait_for()
                    assert mp.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                    mp.screenshot(path=str(OUT / 'mobile-learning.png'), full_page=True)

                    race_context = browser.new_context(viewport={'width': 1440, 'height': 1000})
                    rp = race_context.new_page()
                    rp.on('pageerror', lambda error: errors.append(str(error)))
                    browser_login(rp, '/workspace/', A_EMAIL, A_PASSWORD)
                    rp.locator('[data-action="upload"]').first.wait_for()
                    delayed = []
                    rp.route('**/api/v1/workspace/intakes?delayed=1', lambda route: delayed.append(route))
                    rp.evaluate("() => { window.staleFinished=false; api('/workspace/intakes?delayed=1').catch(()=>{}).finally(()=>window.staleFinished=true); }")
                    rp.locator('.user-button').click()
                    rp.locator('[data-action="logout"]').click()
                    rp.locator('#login-email').fill(B_EMAIL)
                    rp.locator('#login-password').fill(B_PASSWORD)
                    rp.get_by_role('button', name='登录工作空间', exact=True).click()
                    rp.wait_for_function("() => state.authenticated && state.user?.email === 'bob@workspace.test'")
                    assert delayed, 'old workspace request was not held'
                    delayed.pop().fulfill(status=401, content_type='application/json', body=json.dumps({'error': {'code': 'unauthorized', 'message': 'expired old request'}}))
                    rp.wait_for_function('() => window.staleFinished === true')
                    assert rp.locator('#login-root').inner_text() == ''
                    assert rp.locator('#profile-name').inner_text() == '用户 Bob'
                    check('旧账号延迟返回的 401 不会清空新账号的工作台会话')
                    assert not errors, 'Browser page errors: ' + '; '.join(errors)
                    browser.close()
                    check('桌面与移动端无页面脚本错误、无水平溢出')

                stop()
                start(env, log)
                own = call('/client/learning/items/' + ui_item['id'] + '/records', token=a2)
                assert any(r['text'] == '只属于 Alice 的 UI 笔记' for r in own['items'])
                call('/items/' + ui_item['id'], token=bt, expected=404)
                assert len(call('/workspace/sources', token=bt)) == 1
                raw_store = (directory / 'data' / 'workspace.json').read_text(encoding='utf-8')
                for secret in [ADMIN_PASSWORD, A_PASSWORD, B_PASSWORD, a2, bt, sa['access_key'], qa['access_key']]:
                    assert secret not in raw_store, 'plaintext credential persisted'
                check('真实文件存储重启后账号、资料、私人学习数据和隔离关系保持一致，凭据无明文落盘')

                disabled = call('/admin/users/' + a['id'], 'PUT', {'revision': a['revision'], 'email': a['email'], 'display_name': a['display_name'], 'enabled': False}, ADMIN)
                call('/workspace/workspace', token=a2, expected=401)
                call('/items', token=qa['access_key'], expected=401)
                call('/ingest/' + sa['source']['id'] + '/status', token=sa['access_key'], expected=401)
                call('/workspace/workspace', token=bt)
                call('/admin/users/' + a['id'], 'PUT', {'revision': disabled['revision'], 'email': a['email'], 'display_name': a['display_name'], 'enabled': True}, ADMIN)
                call('/workspace/workspace', token=a2, expected=401)
                new_token = login(A_EMAIL, A_PASSWORD)
                assert call('/client/learning/items/' + ui_item['id'] + '/records', token=new_token)['total'] == own['total']
                check('停用用户同时撤销来源/查询访问；重新启用不复活旧会话且不删除资料')
                from setup_ai_push import setup
                token_file = directory / 'explicit-user-token.json'
                token_file.write_text(json.dumps({'access_token': new_token}), encoding='utf-8')
                provisioned = setup(BASE, directory / 'ai-source-config.json', user_token_file=token_file, slug='ai-owned-source')
                assert provisioned['source_authenticated']
                created_sources = call('/workspace/sources', token=new_token)
                assert any(src['id'] == provisioned['source_id'] and src['owner_id'] == a['id'] for src in created_sources)
                check('AI 来源配置脚本仅使用显式用户会话，在本人工作区创建并认证来源')

            finally:
                stop()
    result = {'ok': True, 'verified_at': datetime.now(timezone.utc).isoformat(), 'production_data_touched': False, 'checks': CHECKS, 'count': len(CHECKS), 'postgres_live_test': False,
              'screenshots': ['admin-users.png', 'user-workspace.png', 'user-learning.png', 'mobile-learning.png']}
    (OUT / 'verification.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(f'PASS: {len(CHECKS)} isolated end-to-end checks', flush=True)


if __name__ == '__main__':
    main()
