"""Isolated browser regression for grouped workspace navigation.

Uses a random local port, synthetic accounts and temporary file storage. Never
reads live credentials, changes .local data, or restarts an existing service.
"""
from pathlib import Path
from urllib.error import URLError
import os
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import time

from playwright.sync_api import expect, sync_playwright
from verify_shared_workbench import ROOT, call, login_workbench

if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8')

GROUPS = [
    {'title': '数据管理', 'routes': ['library', 'rss', 'query', 'catalog', 'roadmaps'],
     'labels': ['数据录入', 'RSS 订阅', '数据列表', '合集管理', '学习路线图']},
    {'title': '任务管理', 'routes': ['tasks', 'agent-access'],
     'labels': ['收集任务', 'Agent 接入']},
    {'title': '客户端管理', 'routes': ['clients', 'learning'],
     'labels': ['客户端管理', '学习数据']},
    {'title': '系统管理', 'routes': ['users'], 'labels': ['用户管理']},
]


def check_groups(page, admin=False):
    groups = page.locator('.sidebar > .nav-group-title').evaluate_all('''titles => titles.map(title => ({
        title: title.textContent.trim(),
        routes: [...title.nextElementSibling.querySelectorAll('[data-route]')].map(b => b.dataset.route),
        labels: [...title.nextElementSibling.querySelectorAll('.nav-label')].map(b => b.textContent.trim())
    }))''')
    assert groups == GROUPS, groups
    visible = [group['title'] for group in GROUPS if admin or group['title'] != '系统管理']
    assert page.locator('.nav-group-title:visible').all_text_contents() == visible
    routes = page.locator('.sidebar [data-route]').evaluate_all('buttons => buttons.map(b => b.dataset.route)')
    assert len(routes) == len(set(routes)) == 11
    assert page.locator('.sidebar [data-route="users"]').is_visible() == admin


def check_route(page, route, title, group, click=True):
    if click:
        page.locator(f'.sidebar [data-route="{route}"]').click()
    expect(page.locator('#main-content h1')).to_have_text(title)
    expect(page.locator('.sidebar [aria-current="page"]')).to_have_count(1)
    expect(page.locator(f'.sidebar [data-route="{route}"]')).to_have_attribute('aria-current', 'page')
    breadcrumb = group + ' / ' + title if group != title else title
    expect(page.locator('#breadcrumb-current')).to_have_text(breadcrumb)
    assert page.evaluate('location.hash') == '#' + route
    assert page.title() == title + ' · 温知数据服务'
    expect(page.locator('#login-form')).to_have_count(0)


def main():
    artifacts = ROOT / '.artifacts'
    artifacts.mkdir(exist_ok=True)
    subprocess.run(['node', '--check', 'internal/adminui/app.js'], cwd=ROOT, check=True)
    subprocess.run(['git', 'diff', '--check'], cwd=ROOT, check=True)
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    admin_email, alice_email, bob_email = 'admin@navigation.test', 'alice@navigation.test', 'bob@navigation.test'
    admin_password, alice_password, bob_password = [secrets.token_urlsafe(24) for _ in range(3)]
    admin_key, read_key = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
    with tempfile.TemporaryDirectory(prefix='navigation-groups-', dir=artifacts) as temp:
        directory = Path(temp)
        binary = directory / ('wzstudy.exe' if os.name == 'nt' else 'wzstudy')
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/api'], cwd=ROOT, check=True)
        env = dict(os.environ)
        for name in list(env):
            if name.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):
                env.pop(name)
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(directory / 'data'),
                   HTTP_ADDR=f'127.0.0.1:{port}', ADMIN_API_KEY=admin_key, READ_API_KEY=read_key,
                   SYSTEM_ADMIN_EMAIL=admin_email, SYSTEM_ADMIN_PASSWORD=admin_password,
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
                for email, name, password in [(alice_email, 'Alice', alice_password), (bob_email, 'Bob', bob_password)]:
                    call(base, '/admin/users', 'POST', {'email': email, 'display_name': name, 'password': password}, admin_key)
                alice_token = call(base, '/user/auth/login', 'POST', {'email': alice_email, 'password': alice_password, 'session_mode': 'token'})['access_token']
                bob_token = call(base, '/user/auth/login', 'POST', {'email': bob_email, 'password': bob_password, 'session_mode': 'token'})['access_token']
                clients = {}
                for name, token in [('Alice', alice_token), ('Bob', bob_token)]:
                    clients[name] = call(base, '/workspace/clients', 'POST', {
                        'slug': name.lower() + '-navigation', 'name': name + ' 的客户端',
                        'description': '导航分组隔离测试', 'enabled': True, 'permissions': ['read', 'sync']
                    }, token)['client']
                item = call(base, '/workspace/items', 'POST', {
                    'status': 'pending', 'content': {'kind': 'article', 'title': '导航测试资料', 'content': '真实临时服务上的测试正文。', 'format': 'text'}
                }, alice_token)
                call(base, '/workspace/items/' + item['id'] + '/publish', 'POST', {'revision': item['revision']}, alice_token)

                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    errors, external, mutations = [], [], []
                    def make_page(context):
                        page = context.new_page()
                        page.set_default_timeout(15000)
                        page.on('pageerror', lambda error: errors.append(str(error)))
                        page.on('request', lambda request: external.append(request.url)
                                if request.url.startswith(('http://', 'https://')) and not request.url.startswith(base + '/') else None)
                        return page
                    user_context = browser.new_context(viewport={'width': 1440, 'height': 1100}, reduced_motion='reduce')
                    user = make_page(user_context)
                    login_workbench(user, base, alice_email, alice_password, 'user')
                    user.locator('#home-title').wait_for()
                    check_groups(user)
                    print('PASS: 四个分组和各组顺序准确，首页保留，普通用户隐藏系统管理')
                    for group in GROUPS:
                        if group['title'] == '系统管理':
                            continue
                        for route, title in zip(group['routes'], group['labels']):
                            check_route(user, route, title, group['title'])
                            if route in ['query', 'catalog', 'rss', 'clients', 'roadmaps', 'learning']:
                                expect(user.locator('.section-tabs')).to_have_count(0)
                    print('PASS: 普通用户九个导航入口均可访问，高亮、标题及面包屑与分组一致')
                    check_route(user, 'query', '数据列表', '数据管理')
                    expect(user.locator('#query-results')).to_contain_text('导航测试资料')
                    check_route(user, 'catalog', '合集管理', '数据管理')
                    user.reload()
                    check_route(user, 'catalog', '合集管理', '数据管理', click=False)
                    check_route(user, 'roadmaps', '学习路线图', '数据管理')
                    user.reload()
                    check_route(user, 'roadmaps', '学习路线图', '数据管理', click=False)
                    user.goto(base + '/workspace/#search')
                    check_route(user, 'query', '数据列表', '数据管理', click=False)
                    user.goto(base + '/workspace/#receipts')
                    expect(user.locator('.sidebar [data-route="library"]')).to_have_attribute('aria-current', 'page')
                    expect(user.locator('#breadcrumb-current')).to_have_text('数据管理 / 入库记录')
                    expect(user.locator('.section-tabs')).to_have_count(1)
                    print('PASS: 数据列表读取真实资料，合集和路线图归入数据管理且可深链刷新，旧查询别名及入库记录仍可用')

                    check_route(user, 'learning', '学习数据', '客户端管理')
                    expect(user.locator('.client-row')).to_have_count(1)
                    expect(user.locator('.client-row')).to_contain_text('Alice 的客户端')
                    assert user.get_by_text('Bob 的客户端', exact=True).count() == 0
                    before = user.request.get(base + '/api/v1/workspace/clients/' + clients['Alice']['id'] + '/learning').json()['data']
                    user.on('request', lambda request: mutations.append(request.url)
                            if request.method != 'GET' and '/learning' in request.url else None)
                    user.get_by_role('button', name='查看学习数据', exact=True).click()
                    expect(user.locator('.learning-state-dialog')).to_contain_text('暂无学习数据')
                    user.locator('.learning-state-dialog [data-action="client-learning-events"]').click()
                    expect(user.locator('.learning-state-dialog')).to_contain_text('暂无更多事件')
                    user.locator('.learning-state-dialog [data-action="client-learning"]').click()
                    expect(user.locator('.learning-state-dialog')).to_contain_text('暂无学习数据')
                    user.locator('.learning-state-dialog [data-action="close-modal"]').last.click()
                    user.get_by_role('button', name='事件历史', exact=True).click()
                    expect(user.locator('.learning-state-dialog')).to_contain_text('暂无更多事件')
                    user.locator('.learning-state-dialog [data-action="close-modal"]').last.click()
                    after = user.request.get(base + '/api/v1/workspace/clients/' + clients['Alice']['id'] + '/learning').json()['data']
                    assert before == after
                    assert not mutations, mutations
                    assert user.request.get(base + '/api/v1/workspace/clients/' + clients['Bob']['id'] + '/learning').status == 404
                    assert user.request.get(base + '/api/v1/admin/users').status == 401
                    user.reload()
                    check_route(user, 'learning', '学习数据', '客户端管理', click=False)
                    user.locator('.sidebar').evaluate('el => el.scrollTop = 0')
                    user.screenshot(path=str(artifacts / 'navigation-groups-user.png'), full_page=True)
                    user.goto(base + '/workspace/#users')
                    user.locator('#home-title').wait_for()
                    assert user.evaluate('location.hash') == '#home'
                    print('PASS: 学习数据入口复用只读状态和事件弹窗，跨用户访问被拒绝，不生成学习事件')

                    admin_context = browser.new_context(viewport={'width': 1440, 'height': 1100}, reduced_motion='reduce')
                    admin = make_page(admin_context)
                    login_workbench(admin, base, admin_email, admin_password, 'admin')
                    admin.locator('#home-title').wait_for()
                    check_groups(admin, admin=True)
                    check_route(admin, 'users', '用户管理', '系统管理')
                    check_route(admin, 'learning', '学习数据', '客户端管理')
                    expect(admin.locator('#main-content')).to_contain_text('暂无客户端学习数据')
                    assert admin.get_by_text('Alice 的客户端', exact=True).count() == 0
                    admin.locator('.sidebar').evaluate('el => el.scrollTop = 0')
                    admin.screenshot(path=str(artifacts / 'navigation-groups-admin.png'), full_page=True)
                    admin.set_viewport_size({'width': 1440, 'height': 700})
                    assert admin.locator('.sidebar').evaluate('el => el.scrollHeight > el.clientHeight')
                    admin.locator('.sidebar [data-action="logout"]').scroll_into_view_if_needed()
                    assert admin.locator('.sidebar').evaluate('el => el.scrollTop > 0')
                    print('PASS: 管理员四组均显示，学习数据空态正常，短屏可滚动访问底部菜单')

                    user.set_viewport_size({'width': 900, 'height': 700})
                    check_route(user, 'catalog', '合集管理', '数据管理')
                    assert user.locator('.sidebar .nav-label:visible').count() == 0
                    assert user.locator('.sidebar [data-route="catalog"]').get_attribute('title') == '合集管理'
                    mobile_context = browser.new_context(viewport={'width': 390, 'height': 740}, is_mobile=True, has_touch=True, reduced_motion='reduce')
                    mobile = make_page(mobile_context)
                    login_workbench(mobile, base, alice_email, alice_password, 'user')
                    mobile.locator('#home-title').wait_for()
                    mobile.get_by_role('button', name='打开导航', exact=True).click()
                    mobile.locator('.sidebar [data-route="learning"]').click()
                    check_route(mobile, 'learning', '学习数据', '客户端管理', click=False)
                    expect(mobile.locator('body')).not_to_have_class(re.compile('mobile-open'))
                    assert mobile.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                    mobile.get_by_role('button', name='打开导航', exact=True).click()
                    mobile.locator('.sidebar [data-action="logout"]').scroll_into_view_if_needed()
                    assert mobile.locator('.sidebar').evaluate('el => el.scrollTop > 0')
                    mobile.screenshot(path=str(artifacts / 'navigation-groups-mobile.png'), full_page=True)
                    print('PASS: 平板图标导航与手机抽屉可用，点击后关闭，底部菜单可滚动访问且无横向溢出')
                    assert not errors, errors
                    assert not external, external
                    browser.close()
                    print('PASS: 浏览器无脚本异常、无外部请求；现有服务与数据未修改')
            finally:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=10)


if __name__ == '__main__':
    main()
