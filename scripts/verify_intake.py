"""Verify the three intake paths in Chrome against a disposable real service.
Uses only synthetic accounts, keys and materials; never reads .local or fetches
an external feed. Build .artifacts/wzstudy.exe first. RSS positive ingestion is
covered by internal/feed/intake_test.go with an in-process test transport.
"""
from pathlib import Path
from urllib.error import URLError
from urllib.request import Request, urlopen
from playwright.sync_api import sync_playwright, expect
import io
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import wave

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
ARTIFACTS.mkdir(exist_ok=True)
sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')
CHECKS, ERRORS, EXTERNAL = [], [], []
ADMIN, READ = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
EMAIL, PASSWORD = 'intake@example.test', secrets.token_urlsafe(24)
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    PORT = sock.getsockname()[1]
BASE = f'http://127.0.0.1:{PORT}'
process = None


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def request(path, method='GET', body=None, key=ADMIN):
    headers = {'Authorization': 'Bearer ' + key}
    if body is not None:
        headers['Content-Type'] = 'application/json'
    req = Request(BASE + '/api/v1' + path, method=method, headers=headers,
                  data=json.dumps(body, ensure_ascii=False).encode() if body is not None else None)
    with urlopen(req, timeout=15) as response:
        return json.load(response)['data']


def close_modal(page):
    page.locator('.dialog-footer [data-action="close-modal"]').click()
    expect(page.locator('.dialog')).to_have_count(0)


def nav(page, route):
    main = 'query' if route in ('query', 'clients') else 'library'
    if page.viewport_size['width'] <= 700 and 'mobile-open' not in (page.locator('body').get_attribute('class') or ''):
        page.locator('[data-action="open-nav"]').click()
    page.locator(f'.sidebar [data-route="{main}"]').click()
    if route != main:
        page.locator(f'.section-tabs [data-route="{route}"]').click()
    expect(page.locator('h1')).to_have_text({'library':'数据录入','query':'数据查询','clients':'查询客户端','sources':'录入端管理','access':'接入指引','receipts':'入库记录'}[route])


def source_named(name):
    return next(s for s in request('/admin/workspace')['sources'] if s['name'] == name)


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


def run_skill(temp, source_key, *args):
    env = {k: v for k, v in os.environ.items() if not k.startswith('WZSTUDY_')}
    env['PYTHONIOENCODING'] = 'utf-8'
    command = [sys.executable, str(ROOT / 'skills/wzstudy-push/scripts/wzstudy_push.py'),
               '--config', str(temp / 'client.json'), *args]
    result = subprocess.run(command, cwd=temp, env=env, capture_output=True, encoding='utf-8', timeout=30)
    assert source_key not in result.stdout + result.stderr and ADMIN not in result.stdout + result.stderr
    assert result.returncode == 0, 'Isolated Skill command failed; no raw credential-bearing output printed'
    return json.loads(result.stdout)


try:
    with tempfile.TemporaryDirectory(prefix='intake-', dir=ARTIFACTS) as directory:
        temp = Path(directory)
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_', 'WZSTUDY_'))}
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(temp / 'data'),
                   HTTP_ADDR=f'127.0.0.1:{PORT}', ADMIN_API_KEY=ADMIN, READ_API_KEY=READ,
                   SYSTEM_ADMIN_EMAIL=EMAIL, SYSTEM_ADMIN_PASSWORD=PASSWORD,
                   POLL_ENABLED='false', UPLOAD_MAX_MB='10')
        binary = ARTIFACTS / 'wzstudy.exe'
        if not binary.exists():
            raise RuntimeError('Build .artifacts/wzstudy.exe before verification')
        with (temp / 'service.log').open('ab') as log:
            process = subprocess.Popen([str(binary)], cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT)
            try:
                for _ in range(120):
                    assert process.poll() is None, 'Isolated service exited; log intentionally not printed'
                    try:
                        with urlopen(BASE + '/api/v1/health/ready', timeout=.4) as response:
                            if response.status == 200:
                                break
                    except (URLError, OSError):
                        time.sleep(.1)
                else:
                    raise AssertionError('Isolated service readiness timeout')
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(channel='chrome', headless=True)
                    page = browser.new_page(viewport={'width': 1600, 'height': 1240}, locale='zh-CN')
                    page.set_default_timeout(15000)
                    page.emulate_media(reduced_motion='reduce')
                    page.on('pageerror', lambda error: ERRORS.append(str(error)))
                    page.on('request', lambda r: EXTERNAL.append(r.url)
                            if r.url.startswith(('http://', 'https://')) and not r.url.startswith(BASE + '/') else None)
                    page.goto(BASE + '/admin/')
                    page.locator('#login-email').fill(EMAIL)
                    page.locator('#login-password').fill(PASSWORD)
                    page.locator('#login-form button[type="submit"]').click()
                    expect(page.locator('.intake-card h3')).to_have_text(
                        ['采集程序', 'Skill', 'MCP'])
                    expect(page.locator('#library-results [role="status"]')).to_have_count(0)
                    page.screenshot(path=str(ARTIFACTS / 'intake-desktop.png'), full_page=True)
                    page.set_viewport_size({'width': 390, 'height': 920})
                    assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                    page.screenshot(path=str(ARTIFACTS / 'intake-mobile.png'), full_page=True)
                    page.set_viewport_size({'width': 1600, 'height': 1240})
                    check('三个录入入口并列展示，桌面与手机布局无横向溢出')

                    page.locator('.heading-actions [data-action="upload"]').click()
                    expect(page.locator('#material-type')).to_have_value('article')
                    expect(page.locator('#article-content')).to_be_visible()
                    page.locator('#material-title').fill('手动学习笔记')
                    page.locator('#article-content').fill('直接编写正文，无需上传文件。')
                    page.locator('button[form="upload-form"]').click()
                    expect(page.locator('#nav-count')).to_have_text('1')
                    manual = request('/admin/items?limit=10')['items'][0]
                    assert manual['status'] == 'pending' and manual.get('published') is None
                    assert source_named('管理工作台')['id'] == manual['source_id']
                    check('手动录入默认文章且无需文件，真实保存为待审核并保留系统来源')

                    audio = io.BytesIO()
                    with wave.open(audio, 'wb') as stream:
                        stream.setnchannels(1)
                        stream.setsampwidth(2)
                        stream.setframerate(8000)
                        stream.writeframes(b'\0' * 3200)
                    page.locator('.heading-actions [data-action="upload"]').click()
                    page.locator('#file-input').set_input_files(
                        {'name': '手动音频.wav', 'mimeType': 'audio/wav', 'buffer': audio.getvalue()})
                    expect(page.locator('#material-type')).to_have_value('audio')
                    page.locator('input[name="target"][value="draft"]').check()
                    page.locator('button[form="upload-form"]').click()
                    expect(page.locator('#nav-count')).to_have_text('2')
                    stored_audio = request('/admin/items?kind=audio&limit=10')['items'][0]
                    assert stored_audio['status'] == 'draft' and len(stored_audio['draft']['assets']) == 1
                    check('手动文件自动识别类型、上传实际音频资产并可保存草稿')

                    page.locator('.heading-actions [data-action="collect"]').click()
                    page.locator('#collect-url').fill('https://example.org/manual-source')
                    page.locator('#collect-title').fill('手动登记原始出处')
                    page.locator('#collect-summary').fill('仅登记链接，不访问外部网站。')
                    page.locator('button[form="collect-form"]').click()
                    expect(page.locator('#nav-count')).to_have_text('3')
                    check('手动登记链接仍可用，不冒充正文抓取或媒体下载')

                    page.locator('.intake-card [data-action="new-rss-source"]').click()
                    expect(page.locator('#source-kind')).to_have_value('rss')
                    expect(page.locator('#source-kind')).to_be_disabled()
                    expect(page.locator('#client-permissions')).to_be_hidden()
                    page.locator('#source-name').fill('隔离学习订阅')
                    page.locator('#source-url').fill('https://example.org/feed.xml')
                    page.locator('#source-poll').fill('59')
                    assert page.locator('#source-poll').evaluate('el => el.validity.rangeUnderflow')
                    page.locator('#source-poll').fill('300')
                    page.locator('button[form="source-form"]').click()
                    expect(page.locator('#dialog-title')).to_have_text('RSS 订阅已添加')
                    expect(page.locator('.dialog-body')).to_contain_text('300 秒')
                    expect(page.locator('#one-time-key')).to_have_count(0)
                    rss = source_named('隔离学习订阅')
                    assert rss['kind'] == 'rss' and rss['poll_interval_seconds'] == 300
                    assert rss['enabled'] and not rss['auto_publish'] and not rss.get('last_fetched_at')
                    page.locator('[data-action="manage-sources"]').click()
                    expect(page.locator('#main-content h1')).to_have_text('录入端管理')
                    card = page.locator('.source-card').filter(has_text='隔离学习订阅')
                    card.locator('[data-action="edit-source"]').click()
                    page.locator('#source-poll').fill('600')
                    page.locator('button[form="source-form"]').click()
                    expect(page.locator('.dialog')).to_have_count(0)
                    assert source_named('隔离学习订阅')['poll_interval_seconds'] == 600
                    check('RSS 专用入口保存订阅与合法间隔，默认待审核，配置不伪称已抓取且可修改')

                    page.locator('.heading-actions [data-action="new-source"]').click()
                    page.locator('#source-kind').select_option('manual')
                    expect(page.locator('#source-poll')).to_be_disabled()
                    expect(page.locator('#client-permissions input').first).to_be_enabled()
                    page.locator('#source-kind').select_option('rss')
                    expect(page.locator('#source-poll')).to_be_enabled()
                    expect(page.locator('#client-permissions input').first).to_be_disabled()
                    close_modal(page)
                    check('通用来源入口在 RSS 与客户端间切换时正确切换必填项与权限控件')

                    nav(page, 'library')
                    page.locator('.intake-card [data-action="ai-skill"]').click()
                    expect(page.locator('#ai-source-select')).to_be_disabled()
                    expect(page.locator('#ai-skill-guide')).to_contain_text('不包含真实 Key')
                    page.locator('[data-action="new-ai-source"]').click()
                    expect(page.locator('#source-kind')).to_have_value('manual')
                    expect(page.locator('#source-kind')).to_be_disabled()
                    expect(page.locator('#permission-preset')).to_have_value('collector')
                    assert page.locator('input[name="permissions"]:checked').evaluate_all(
                        'nodes => nodes.map(n => n.value)') == ['ingest', 'upload']
                    expect(page.locator('#source-poll')).to_be_disabled()
                    page.locator('#source-name').fill('AI 采集 <测试>')
                    page.locator('button[form="source-form"]').click()
                    expect(page.locator('#one-time-key')).to_be_visible()
                    source_key = page.locator('#one-time-key').input_value()
                    assert source_key.startswith('wzsk_')
                    source = source_named('AI 采集 <测试>')
                    assert source['permissions'] == ['ingest', 'upload'] and source['integration'] == 'skill' and not source['auto_publish']
                    page.locator('.dialog-footer [data-action="ai-skill"]').click()
                    expect(page.locator('#ai-source-select')).to_have_value(source['id'])
                    template = json.loads(page.locator('#ai-config-template').input_value())
                    assert template['base_url'] == BASE and template['source_id'] == source['id']
                    assert source_key not in page.locator('#ai-skill-guide').inner_text()
                    assert template['source_key'] != source_key
                    expect(page.locator('#ai-skill-guide')).to_contain_text('--dry-run')
                    page.screenshot(path=str(ARTIFACTS / 'intake-ai-guide.png'), full_page=True)
                    page.set_viewport_size({'width': 390, 'height': 920})
                    assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                    page.screenshot(path=str(ARTIFACTS / 'intake-ai-mobile.png'), full_page=True)
                    page.set_viewport_size({'width': 1600, 'height': 1240})
                    check('AI 专用来源默认最小权限，模板准确且无真实 Key，接入引导兼容手机')

                    config_path = temp / 'client.json'
                    config_path.write_text(json.dumps({'base_url': BASE, 'source_id': source['id'],
                                                       'source_key': source_key}), encoding='utf-8')
                    inputs = temp / 'items.json'
                    inputs.write_text(json.dumps({'items': [{'external_id': 'intake:ai-1',
                        'title': 'AI 整理学习摘要', 'content': '隔离验证的有出处学习摘要。',
                        'url': 'https://example.org/ai-source', 'format': 'markdown'}]},
                        ensure_ascii=False), encoding='utf-8')
                    assert run_skill(temp, source_key, 'doctor')['source_authenticated']
                    dry = run_skill(temp, source_key, 'push', '--input', str(inputs), '--dry-run')
                    assert dry['items'] == 1 and dry['network_requests'] == 0
                    first = run_skill(temp, source_key, 'push', '--input', str(inputs))['batches'][0]
                    second = run_skill(temp, source_key, 'push', '--input', str(inputs))['batches'][0]
                    assert first['created'] == 1 and first['published'] == 0
                    assert second['unchanged'] == 1 and second['items'][0]['id'] == first['items'][0]['id']
                    ai_item = request('/admin/items/' + first['items'][0]['id'])
                    assert ai_item['status'] == 'pending' and ai_item['source_id'] == source['id']
                    assert request('/items', key=READ)['items'] == []
                    check('由页面创建的最小权限来源可通过真实 Skill 检查、预检、入库和去重，待审核内容不公开')

                    page.locator('.dialog-footer [data-action="edit-source"]').click()
                    page.locator('input[name="permissions"][value="upload"]').uncheck()
                    page.locator('button[form="source-form"]').click()
                    expect(page.locator('.dialog')).to_have_count(0)
                    page.locator('.intake-card [data-action="ai-skill"]').click()
                    expect(page.locator('#ai-skill-guide .source-error')).to_contain_text('上传媒体还需')
                    close_modal(page)
                    current = source_named('AI 采集 <测试>')
                    update = {k: current[k] for k in ['slug', 'name', 'description', 'kind', 'category',
                              'language', 'enabled', 'auto_publish', 'poll_interval_seconds', 'permissions', 'revision']}
                    update['auto_publish'] = True
                    update['enabled'] = False
                    request('/admin/sources/' + current['id'], 'PUT', update)
                    page.reload()
                    page.locator('.intake-card [data-action="ai-skill"]').click()
                    expect(page.locator('#ai-skill-guide')).to_contain_text('此来源已停用')
                    expect(page.locator('#ai-skill-guide')).to_contain_text('此来源已启用自动发布')
                    close_modal(page)
                    check('已有来源缺上传权限、已停用或启用自动发布时准确提示，不误报统一待审核')
                    assert not ERRORS, ERRORS
                    assert not EXTERNAL, 'Browser unexpectedly requested an external website'
                    check('浏览器无 JavaScript 异常，验证未请求真实外站或真实用户资料库')
                    browser.close()
            finally:
                stop()
except Exception:
    report = {'result': 'failed', 'checks': CHECKS, 'browser_errors': ERRORS,
              'workspace': 'isolated temporary store; no real credentials read'}
    (ARTIFACTS / 'intake-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    raise

report = {'result': 'passed', 'groups': len(CHECKS), 'checks': CHECKS,
          'workspace': 'isolated temporary file store; synthetic accounts, keys and materials only',
          'rss_positive_ingestion': 'covered by Go test transport; no live external feed requested'}
(ARTIFACTS / 'intake-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
