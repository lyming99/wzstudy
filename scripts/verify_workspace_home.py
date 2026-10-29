"""Browser regression for the role-aware workbench home.

Runs only against a temporary file store and a random local port. Existing
workspaces and the running service are never read or modified. Set
WZSTUDY_HOME_SCREENSHOTS to a directory to save synthetic-fixture screenshots.
"""
from pathlib import Path
from urllib.error import URLError
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time

from verify_shared_workbench import ROOT, call, login_workbench

if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8')


def main():
    from playwright.sync_api import sync_playwright
    artifacts = ROOT / '.artifacts'
    artifacts.mkdir(exist_ok=True)
    screenshots = os.environ.get('WZSTUDY_HOME_SCREENSHOTS')
    capture_dir = Path(screenshots) if screenshots else None
    if capture_dir:
        capture_dir.mkdir(parents=True, exist_ok=True)
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    admin_email = 'admin@home.test'
    admin_password = secrets.token_urlsafe(24)
    admin_key, read_key = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
    alice_password, bob_password, empty_password = [secrets.token_urlsafe(24) for _ in range(3)]
    with tempfile.TemporaryDirectory(prefix='home-verification-', dir=artifacts) as temp:
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
                for email, name, password in [('alice@home.test', 'Alice', alice_password), ('bob@home.test', 'Bob', bob_password), ('empty@home.test', '新用户', empty_password)]:
                    call(base, '/admin/users', 'POST', {'email':email, 'display_name':name, 'password':password}, admin_key)
                def token(email, password):
                    return call(base, '/user/auth/login', 'POST', {'email':email, 'password':password, 'session_mode':'token'})['access_token']
                alice_token, bob_token = token('alice@home.test', alice_password), token('bob@home.test', bob_password)
                def item(owner, title, status='draft'):
                    return call(base, '/workspace/items', 'POST', {'status':status, 'content':{'kind':'article', 'title':title, 'content':'隔离测试正文。', 'format':'text'}}, owner)
                published = item(alice_token, 'Alice 已发布资料', 'pending')
                call(base, '/workspace/items/' + published['id'] + '/publish', 'POST', {'revision':published['revision']}, alice_token)
                item(alice_token, 'Alice 私人草稿')
                item(bob_token, 'Bob 私人资料')
                alice_summary = call(base, '/workspace/workspace', key=alice_token)['summary']
                bob_summary = call(base, '/workspace/workspace', key=bob_token)['summary']
                assert alice_summary['total'] == 2 and alice_summary['published'] == 1
                assert bob_summary['total'] == 1
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    errors = []
                    def make_page(context):
                        page = context.new_page()
                        page.on('pageerror', lambda error: errors.append(str(error)))
                        return page
                    def metric(page, label):
                        return page.locator('.home-metric').filter(has_text=label).locator('strong').inner_text()

                    alice_context = browser.new_context(viewport={'width':1440, 'height':1000})
                    alice = make_page(alice_context)
                    alice.goto(base + '/')
                    alice.locator('#login-form').wait_for()
                    assert alice.evaluate('location.pathname') == '/workspace/'
                    login_workbench(alice, base, 'alice@home.test', alice_password)
                    alice.locator('#home-title').filter(has_text='Alice').wait_for()
                    alice.goto(base + '/')
                    alice.locator('#home-title').filter(has_text='Alice').wait_for()
                    assert alice.evaluate('location.pathname') == '/workspace/'
                    assert alice.evaluate('location.hash') == '#home'
                    print('PASS: 根地址在登录前打开工作台登录页，登录后打开首页')
                    assert alice.locator('.sidebar [data-route="home"]').get_attribute('aria-current') == 'page'
                    assert metric(alice, '资料总量') == str(alice_summary['total'])
                    assert metric(alice, '已发布') == str(alice_summary['published'])
                    assert metric(alice, '待审核') == str(alice_summary['pending'])
                    assert metric(alice, '近 7 天新增') == str(alice_summary['week_new'])
                    assert alice.get_by_text('Alice 私人草稿', exact=True).is_visible()
                    assert alice.get_by_text('Bob 私人资料', exact=True).count() == 0
                    assert not alice.locator('.sidebar [data-route="users"]').is_visible()
                    assert alice.request.get(base + '/api/v1/admin/users').status == 401
                    if capture_dir:
                        alice.screenshot(path=str(capture_dir / 'home-user.png'), full_page=True)
                    alice.get_by_role('button', name='查看 Alice 私人草稿', exact=True).click()
                    alice.get_by_role('heading', name='Alice 私人草稿', exact=True).wait_for()
                    alice.locator('[data-action="close-drawer"]').first.click()
                    alice.locator('.home-shortcut[data-route="library"]').click()
                    alice.get_by_role('heading', name='数据录入', exact=True).wait_for()
                    alice.locator('.sidebar [data-route="home"]').click()
                    alice.locator('#home-title').wait_for()
                    alice.reload()
                    alice.locator('#home-title').filter(has_text='Alice').wait_for()
                    alice.goto(base + '/workspace/#roadmaps')
                    alice.get_by_role('heading', name='学习路线图', exact=True).wait_for()
                    assert alice.evaluate('location.hash') == '#roadmaps'
                    alice.goto(base + '/workspace/#users')
                    alice.locator('#home-title').wait_for()
                    assert alice.evaluate('location.hash') == '#home'
                    print('PASS: 普通用户首页显示真实汇总、本人近期资料及可用入口；深链与无权路由正确')

                    bob_context = browser.new_context(viewport={'width':1440, 'height':1000})
                    bob = make_page(bob_context)
                    login_workbench(bob, base, 'bob@home.test', bob_password)
                    bob.locator('#home-title').filter(has_text='Bob').wait_for()
                    assert metric(bob, '资料总量') == str(bob_summary['total'])
                    assert bob.get_by_text('Bob 私人资料', exact=True).is_visible()
                    assert bob.get_by_text('Alice 私人草稿', exact=True).count() == 0
                    print('PASS: 不同用户首页的统计和最近资料保持隔离')

                    admin_context = browser.new_context(viewport={'width':1440, 'height':1000})
                    admin = make_page(admin_context)
                    admin.route('**/api/v1/admin/users', lambda route: route.fulfill(status=500, content_type='application/json', body='{"error":{"message":"测试读取失败"}}'))
                    login_workbench(admin, base, admin_email, admin_password, 'admin')
                    admin.locator('#home-title').wait_for()
                    admin.get_by_text('暂时无法读取账号概览', exact=False).wait_for()
                    assert admin.locator('#home-admin-total').inner_text() == '—'
                    admin.unroute('**/api/v1/admin/users')
                    admin.get_by_role('button', name='重新加载', exact=True).click()
                    admin.locator('#home-admin-total').filter(has_text='3').wait_for()
                    assert admin.locator('#home-admin-enabled').inner_text() == '3'
                    assert admin.locator('#home-admin-disabled').inner_text() == '0'
                    assert not admin.locator('.sidebar [data-route="library"]').is_visible()
                    assert admin.locator('.home-recent-row').count() == 0
                    assert admin.request.get(base + '/api/v1/workspace/workspace').status == 401
                    if capture_dir:
                        admin.screenshot(path=str(capture_dir / 'home-admin.png'), full_page=True)
                    admin.locator('.home-shortcut[data-route="users"]').click()
                    admin.get_by_role('heading', name='用户管理', exact=True).wait_for()
                    admin.locator('.sidebar [data-route="home"]').click()
                    admin.locator('#home-admin-total').filter(has_text='3').wait_for()
                    print('PASS: 管理员首页显示真实账号数，失败可重试，不读取个人资料')

                    mobile_context = browser.new_context(viewport={'width':390, 'height':844}, is_mobile=True, has_touch=True)
                    mobile = make_page(mobile_context)
                    login_workbench(mobile, base, 'empty@home.test', empty_password)
                    mobile.locator('#home-title').filter(has_text='新用户').wait_for()
                    assert mobile.get_by_text('这里还没有资料', exact=True).is_visible()
                    assert mobile.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                    if capture_dir:
                        mobile.screenshot(path=str(capture_dir / 'home-mobile.png'), full_page=True)
                    mobile.get_by_role('button', name='打开导航', exact=True).click()
                    mobile.locator('.sidebar [data-route="library"]').click()
                    mobile.get_by_role('heading', name='数据录入', exact=True).wait_for()
                    assert not errors, 'browser errors: ' + '; '.join(errors)
                    browser.close()
                    print('PASS: 空工作区与手机首页布局、导航和无横向溢出正常')
            finally:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()
    print('PASS: 首页验证未触及现有 .local 数据或运行中的 8091 服务')
    if capture_dir:
        print('SCREENSHOTS:', capture_dir)


if __name__ == '__main__':
    main()
