"""Isolated browser/HTTP regression for the shared, role-separated workbench.

Builds an isolated executable and starts a random-port service with temporary
file storage. Never reads or changes the user's .local data or running service.
"""
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import URLError
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
ARTIFACTS.mkdir(exist_ok=True)
if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8')

ADMIN_EMAIL = 'admin@shared.test'
ADMIN_PASSWORD = secrets.token_urlsafe(24)
USER_EMAIL = 'learner@shared.test'
USER_PASSWORD = secrets.token_urlsafe(24)
ADMIN_KEY = secrets.token_urlsafe(32)
READ_KEY = secrets.token_urlsafe(32)


def call(base, path, method='GET', body=None, key=None):
    headers = {}
    raw = None
    if body is not None:
        headers['Content-Type'] = 'application/json'
        raw = json.dumps(body).encode('utf-8')
    if key:
        headers['Authorization'] = 'Bearer ' + key
    with urlopen(Request(base + '/api/v1' + path, data=raw, headers=headers, method=method), timeout=10) as response:
        return json.loads(response.read())['data']


def login_workbench(page, base, email, password, expected_role):
    page.goto(base + '/workspace/')
    page.locator('#login-form').wait_for()
    assert page.get_by_label('登录身份').count() == 0
    page.locator('#login-email').fill(email)
    page.locator('#login-password').fill(password)
    with page.expect_response(lambda response: response.url.endswith('/api/v1/auth/login') and response.request.method == 'POST') as pending:
        page.get_by_role('button', name='登录工作空间', exact=True).click()
    response = pending.value
    assert response.ok and response.json()['data']['role'] == expected_role
    assert set(response.request.post_data_json) == {'email', 'password'}


def main():
    from playwright.sync_api import sync_playwright
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    with tempfile.TemporaryDirectory(prefix='shared-workbench-', dir=ARTIFACTS) as temp:
        directory = Path(temp)
        binary = directory / ('wzstudy.exe' if os.name == 'nt' else 'wzstudy')
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/api'], cwd=ROOT, check=True)
        env = dict(os.environ)
        for name in list(env):
            if name.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):
                env.pop(name)
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(directory / 'data'),
                   HTTP_ADDR=f'127.0.0.1:{port}', ADMIN_API_KEY=ADMIN_KEY, READ_API_KEY=READ_KEY,
                   SYSTEM_ADMIN_EMAIL=ADMIN_EMAIL, SYSTEM_ADMIN_PASSWORD=ADMIN_PASSWORD,
                   POLL_ENABLED='false', CORS_ORIGINS='', UPLOAD_MAX_MB='10')
        with (directory / 'service.log').open('ab') as log:
            server = subprocess.Popen([str(binary)], cwd=ROOT, env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    if server.poll() is not None:
                        raise RuntimeError('isolated service exited before readiness')
                    try:
                        call(base, '/health/ready')
                        break
                    except (URLError, OSError):
                        time.sleep(.1)
                else:
                    raise RuntimeError('isolated service readiness timeout')
                call(base, '/admin/users', 'POST', {'email': USER_EMAIL, 'display_name': '学习用户', 'password': USER_PASSWORD}, ADMIN_KEY)
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    errors = []
                    user_context = browser.new_context(viewport={'width': 1440, 'height': 1000})
                    user_page = user_context.new_page()
                    user_page.on('pageerror', lambda error: errors.append(str(error)))
                    login_workbench(user_page, base, USER_EMAIL, USER_PASSWORD, 'user')
                    user_page.locator('[data-action="upload"]').first.wait_for()
                    assert user_page.request.get(base + '/api/v1/admin/users').status == 401
                    assert not user_page.locator('.sidebar [data-route="users"]').is_visible()
                    user_page.locator('[data-action="upload"]').first.click()
                    user_page.locator('#material-title').fill('普通用户私人资料')
                    user_page.locator('#article-content').fill('只属于普通用户的内容')
                    user_page.locator('button[form="upload-form"]').click()
                    user_page.locator('#modal-root').wait_for(state='hidden')
                    user_items = user_page.request.get(base + '/api/v1/workspace/items?q=普通用户私人资料').json()['data']['items']
                    assert len(user_items) == 1
                    print('PASS: 普通用户使用同一登录表单和接口，服务端判定角色且不能访问管理接口')
                    context = browser.new_context(viewport={'width': 1440, 'height': 1000})
                    page = context.new_page()
                    page.on('pageerror', lambda error: errors.append(str(error)))
                    login_workbench(page, base, ADMIN_EMAIL, ADMIN_PASSWORD, 'admin')
                    page.locator('#home-title').wait_for()
                    page.locator('#home-admin-total').filter(has_text='1').wait_for()
                    assert page.locator('.sidebar [data-route="library"]').is_visible()
                    assert page.locator('.sidebar [data-route="users"]').is_visible()
                    assert page.request.get(base + '/api/v1/workspace/workspace').status == 200
                    assert page.request.get(base + '/api/v1/workspace/items/' + user_items[0]['id']).status == 404
                    page.locator('[data-action="upload"]').first.click()
                    page.locator('#material-title').fill('管理员自己的资料')
                    page.locator('#article-content').fill('只属于管理员个人工作区')
                    page.locator('button[form="upload-form"]').click()
                    page.locator('#modal-root').wait_for(state='hidden')
                    own_items = page.request.get(base + '/api/v1/workspace/items?q=管理员自己的资料').json()['data']['items']
                    assert len(own_items) == 1
                    assert user_page.request.get(base + '/api/v1/workspace/items/' + own_items[0]['id']).status == 404
                    user_context.close()
                    page.locator('.sidebar [data-route="users"]').click()
                    page.get_by_role('heading', name='用户管理', exact=True).wait_for()
                    page.get_by_text(USER_EMAIL, exact=True).wait_for()
                    assert page.locator('.sidebar [data-route="users"]').is_visible()
                    assert page.locator('.sidebar [data-route="library"]').is_visible()
                    assert page.request.get(base + '/api/v1/admin/users').status == 200
                    assert page.request.get(base + '/api/v1/workspace/workspace').status == 200
                    page.reload()
                    page.get_by_role('heading', name='用户管理', exact=True).wait_for()
                    print('PASS: 管理员兼具本人工作区和用户管理；两账号的资料互相隔离且刷新保持权限')

                    page.get_by_role('button', name='创建用户', exact=True).click()
                    managed_password = secrets.token_urlsafe(20)
                    page.locator('#admin-user-email').fill('managed@shared.test')
                    page.locator('#admin-user-name').fill('待管理用户')
                    page.locator('#admin-user-password').fill(managed_password)
                    page.get_by_role('button', name='保存账号', exact=True).click()
                    row = page.locator('.admin-user-row').filter(has_text='managed@shared.test')
                    row.wait_for()
                    user_id = row.get_attribute('data-admin-user')
                    pattern = '**/api/v1/admin/users/' + user_id
                    held = []
                    page.route(pattern, lambda route: held.append(route))
                    row.get_by_role('button', name='编辑 / 重置密码', exact=True).click()
                    page.locator('#admin-user-name').fill('已修改用户')
                    page.get_by_role('button', name='保存账号', exact=True).click()
                    page.locator('#admin-user-form[data-submitting="true"]').wait_for()
                    page.get_by_role('button', name='创建用户', exact=True).click()
                    assert page.locator('#admin-user-form').get_attribute('data-id') == user_id
                    assert held, 'admin save interception missing'
                    held.pop().continue_()
                    row.get_by_text('已修改用户', exact=True).wait_for()
                    page.unroute(pattern)
                    reset_password = secrets.token_urlsafe(20)
                    row.get_by_role('button', name='编辑 / 重置密码', exact=True).click()
                    page.locator('#admin-user-password').fill(reset_password)
                    page.get_by_role('button', name='保存账号', exact=True).click()
                    page.locator('#admin-user-form-panel').wait_for(state='hidden')
                    assert page.request.post(base + '/api/v1/user/auth/login', data={'email':'managed@shared.test', 'password':managed_password, 'session_mode':'token'}).status == 401
                    assert page.request.post(base + '/api/v1/user/auth/login', data={'email':'managed@shared.test', 'password':reset_password, 'session_mode':'token'}).status == 200
                    assert page.request.get(base + '/api/v1/admin/users').status == 200
                    page.once('dialog', lambda dialog: dialog.accept())
                    row.get_by_role('button', name='停用', exact=True).click()
                    row.get_by_text('已停用', exact=True).wait_for()
                    page.once('dialog', lambda dialog: dialog.accept())
                    row.get_by_role('button', name='启用', exact=True).click()
                    row.get_by_text('已启用', exact=True).wait_for()
                    print('PASS: 同一工作台完成用户创建、编辑、停用及启用')

                    learn = context.new_page()
                    learn.on('pageerror', lambda error: errors.append(str(error)))
                    learn.goto(base + '/learn/')
                    learn.get_by_role('heading', name='在自己的知识库里，持续进步', exact=True).wait_for()
                    assert learn.get_by_text(ADMIN_EMAIL, exact=True).is_visible()
                    learn.get_by_role('button', name='退出', exact=True).click()
                    learn.locator('#login').wait_for()
                    learn.get_by_label('用户邮箱').fill(USER_EMAIL)
                    learn.get_by_label('登录密码').fill(USER_PASSWORD)
                    learn.get_by_role('button', name='进入个人学习', exact=True).click()
                    learn.get_by_role('heading', name='在自己的知识库里，持续进步', exact=True).wait_for()
                    page.locator('#login-form').wait_for()
                    assert page.locator('#main-content').inner_text() == ''
                    assert page.request.get(base + '/api/v1/admin/users').status == 401
                    print('PASS: 同浏览器切换为普通用户会撤销旧管理会话并清除其他标签的管理界面')

                    learner = context.new_page()
                    learner.on('pageerror', lambda error: errors.append(str(error)))
                    learner.goto(base + '/workspace/#users')
                    learner.locator('[data-action="upload"]').first.wait_for()
                    assert not learner.locator('.sidebar [data-route="users"]').is_visible()
                    assert learner.locator('.sidebar [data-route="library"]').is_visible()
                    assert learner.evaluate('location.hash') == '#home'
                    assert learner.request.get(base + '/api/v1/admin/users').status == 401
                    learner.reload()
                    learner.locator('[data-action="upload"]').first.wait_for()
                    assert not learner.locator('.sidebar [data-route="users"]').is_visible()
                    print('PASS: 普通用户隐藏管理视图，直接访问管理接口被拒绝且不能用路由绕过')

                    legacy = context.new_page()
                    legacy.on('pageerror', lambda error: errors.append(str(error)))
                    legacy.goto(base + '/admin/')
                    legacy.get_by_label('登录邮箱').fill(ADMIN_EMAIL)
                    legacy.get_by_label('登录密码').fill(ADMIN_PASSWORD)
                    legacy.get_by_role('button', name='登录', exact=True).click()
                    legacy.get_by_text(USER_EMAIL, exact=True).wait_for()
                    learner.locator('#login-form').wait_for()
                    learn.locator('#login').wait_for()
                    assert learner.request.get(base + '/api/v1/user/auth/session').status == 401
                    shared_admin = context.new_page()
                    shared_admin.on('pageerror', lambda error: errors.append(str(error)))
                    shared_admin.goto(base + '/workspace/')
                    shared_admin.locator('#home-title').wait_for()
                    shared_admin.locator('#home-admin-total').filter(has_text='2').wait_for()
                    shared_admin.get_by_role('button', name='退出登录', exact=True).click()
                    shared_admin.locator('#login-form').wait_for()
                    legacy.locator('#admin-login').wait_for()
                    print('PASS: 管理员切换会撤销旧用户会话，旧入口和统一界面跨标签同步退出')

                    mobile = browser.new_context(viewport={'width': 390, 'height': 844}, is_mobile=True, has_touch=True)
                    mobile_page = mobile.new_page()
                    mobile_page.on('pageerror', lambda error: errors.append(str(error)))
                    login_workbench(mobile_page, base, ADMIN_EMAIL, ADMIN_PASSWORD, 'admin')
                    mobile_page.locator('#home-title').wait_for()
                    assert mobile_page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                    mobile_page.get_by_role('button', name='打开导航', exact=True).click()
                    mobile_page.locator('.sidebar [data-route="users"]').click()
                    mobile_page.get_by_role('heading', name='用户管理', exact=True).wait_for()
                    assert mobile_page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                    assert not errors, 'browser errors: ' + '; '.join(errors)
                    browser.close()
                    print('PASS: 管理视图支持移动宽度且无页面脚本错误')
            finally:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()
    print('PASS: 隔离测试未使用现有 .local 数据或运行中的 8091 服务')


if __name__ == '__main__':
    main()
