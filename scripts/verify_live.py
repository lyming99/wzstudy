"""Run the real Go service with isolated file storage and verify it in Chrome.
No existing keys, databases or user data are read. No external websites are fetched.
Requires an already built .artifacts/wzstudy.exe and Python Playwright.
"""
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError
from playwright.sync_api import sync_playwright, expect
import base64
import csv
import hashlib
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

sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')
ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
ARTIFACTS.mkdir(exist_ok=True)
CHECKS, ERRORS, EXTERNAL = [], [], []
ADMIN = secrets.token_hex(24)
READ = secrets.token_hex(24)
EMAIL = "admin@example.test"
PASSWORD = secrets.token_urlsafe(24)
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    PORT = sock.getsockname()[1]
BASE = f'http://127.0.0.1:{PORT}'
process = None


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def request(path, method='GET', body=None, key=ADMIN):
    headers = {'Authorization': 'Bearer ' + key} if key else {}
    if body is not None:
        headers['Content-Type'] = 'application/json'
    req = Request(BASE + '/api/v1' + path, method=method, headers=headers,
                  data=json.dumps(body, ensure_ascii=False).encode() if body is not None else None)
    with urlopen(req, timeout=40) as response:
        return json.load(response)['data']


def nav(page, route):
    main = 'query' if route in ('query', 'clients') else 'library'
    if page.viewport_size['width'] <= 700 and 'mobile-open' not in (page.locator('body').get_attribute('class') or ''):
        page.locator('[data-action="open-nav"]').click()
    page.locator(f'.sidebar [data-route="{main}"]').click()
    if route != main:
        page.locator(f'.section-tabs [data-route="{route}"]').click()
    expect(page.locator('h1')).to_have_text({'library':'数据录入','query':'数据查询','clients':'查询客户端','sources':'录入端管理','access':'接入指引','receipts':'入库记录'}[route])


def login(page):
    expect(page.locator('#login-password')).to_be_visible()
    page.locator('#login-email').fill(EMAIL)
    page.locator('#login-password').fill(PASSWORD)
    page.locator('#login-form button[type="submit"]').click()
    expect(page.locator('#login-root')).to_be_empty()
    expect(page.locator('#main-content h1')).to_have_text('数据录入')
    expect(page.locator('#main-content h1')).to_be_visible()
    expect(page.locator('#library-results [role="status"]')).to_have_count(0)


def wait_js(page, expression, timeout=15):
    # Poll through CDP's normal function evaluation. Do not relax the app CSP
    # or rely on wait_for_function's string-eval implementation.
    deadline = time.time() + timeout
    while time.time() < deadline:
        if page.evaluate('() => (' + expression + ')'):
            return
        page.wait_for_timeout(80)
    diagnostic = page.evaluate("() => {const v=document.querySelector('video');return v?{src:v.currentSrc,ready:v.readyState,network:v.networkState,error:v.error?{code:v.error.code,message:v.error.message}:null,width:v.videoWidth,height:v.videoHeight}:null}")
    print('MEDIA_DIAGNOSTIC:', json.dumps(diagnostic, ensure_ascii=False), flush=True)
    page.screenshot(path=str(ARTIFACTS / 'live-failure.png'), full_page=True)
    raise AssertionError('Browser condition timed out: ' + expression)


def close_modal(page):
    page.locator('.dialog-footer [data-action="close-modal"]').click()


def detail(page, title):
    page.locator('.materials-table .material-name', has_text=title).click()
    expect(page.locator('.drawer-title')).to_contain_text(title)


def approve(page):
    page.locator('.drawer-footer [data-action="publish"]').click()
    expect(page.locator('.drawer-scroll > .badge')).to_have_text('已发布')


def make_png(page, title, color):
    data = page.evaluate("""([title,color])=>{const c=document.createElement('canvas');c.width=600;c.height=800;const x=c.getContext('2d');x.fillStyle=color;x.fillRect(0,0,600,800);x.fillStyle='#526b49';x.font='25px sans-serif';x.fillText('A LITTLE LEARNING, EVERY DAY',46,80);x.font='40px sans-serif';x.fillText(title,46,350);x.font='22px sans-serif';x.fillText('让知识，走向真实的行动。',46,430);return c.toDataURL('image/png').split(',')[1]}""", [title, color])
    return base64.b64decode(data)


def start(data, log, bootstrap=True):
    global process
    env = {**os.environ, 'SYSTEM_ADMIN_EMAIL': EMAIL if bootstrap else '', 'SYSTEM_ADMIN_PASSWORD': PASSWORD if bootstrap else '', 'BOOTSTRAP_ADMIN_EMAIL': '', 'BOOTSTRAP_ADMIN_PASSWORD': '', 'APP_ENV': 'test', 'STORE': 'file', 'DATA_DIR': str(data), 'HTTP_ADDR': f'127.0.0.1:{PORT}', 'ADMIN_API_KEY': ADMIN, 'READ_API_KEY': READ, 'POLL_ENABLED': 'false', 'UPLOAD_MAX_MB': '10'}
    process = subprocess.Popen([str(ARTIFACTS / 'wzstudy.exe')], cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT)
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError('Go server exited; inspect .artifacts/live-service.log')
        try:
            with urlopen(BASE + '/api/v1/health/ready', timeout=0.5) as response:
                if response.status == 200:
                    return
        except (URLError, OSError):
            time.sleep(0.1)
    raise RuntimeError('Go server did not become ready')


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


try:
    with tempfile.TemporaryDirectory(prefix='live-data-', dir=ARTIFACTS) as temp, (ARTIFACTS / 'live-service.log').open('ab') as log:
        start(Path(temp), log)
        with sync_playwright() as pw:
            browser = pw.chromium.launch(channel='chrome', headless=True, args=['--autoplay-policy=no-user-gesture-required'])
            context = browser.new_context(viewport={'width': 1600, 'height': 1240}, locale='zh-CN', accept_downloads=True)
            page = context.new_page()
            page.set_default_timeout(15000)
            page.on('pageerror', lambda error: ERRORS.append(str(error)))
            page.on('request', lambda r: EXTERNAL.append(r.url) if r.url.startswith(('http://', 'https://')) and not r.url.startswith(BASE + '/') else None)
            page.goto(BASE + '/admin/')
            expect(page.locator('#login-email')).to_be_visible()
            expect(page.locator('#access-key')).to_have_count(0)
            page.screenshot(path=str(ARTIFACTS / 'login-email-desktop.png'), full_page=True)
            page.set_viewport_size({'width':390,'height':920})
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
            page.screenshot(path=str(ARTIFACTS / 'login-email-mobile.png'), full_page=True)
            page.set_viewport_size({'width':1600,'height':1240})
            check('管理员邮箱/密码登录表单、无密钥登录入口与手机端布局')
            page.locator('#login-email').fill(EMAIL)
            page.locator('#login-password').fill('x' * 40)
            page.locator('#login-form button[type="submit"]').click()
            expect(page.locator('#login-error')).to_contain_text('邮箱或密码不正确')
            login(page)
            expect(page.locator('#nav-count')).to_have_text('0')
            expect(page.locator('.empty h3')).to_have_text('让第一份好内容，在这里安家')
            assert page.evaluate("document.cookie.includes('wzstudy_admin')") is False
            check('邮箱密码登录、错误凭据、HttpOnly 会话与无演示数据的空工作区')

            nav(page, 'sources')
            page.locator('.heading-actions [data-action="new-source"]').click()
            page.locator('#source-name').fill('桌面采集程序')
            page.locator('#source-desc').fill('统一资料录入专用身份')
            page.locator('button[form="source-form"]').click()
            expect(page.locator('#one-time-key')).to_be_visible()
            source_key = page.locator('#one-time-key').input_value()
            assert source_key.startswith('wzsk_')
            workspace = request('/admin/workspace')
            client = next(s for s in workspace['sources'] if s['name'] == '桌面采集程序')
            close_modal(page)
            nav(page, 'clients')
            page.locator('.heading-actions [data-action="new-client"]').click()
            page.locator('#client-name').fill('桌面查询客户端')
            page.locator('button[form="client-form"]').click()
            expect(page.locator('#one-time-key')).to_be_visible()
            query_key = page.locator('#one-time-key').input_value()
            assert query_key.startswith('wzck_') and query_key != source_key
            close_modal(page)
            expect(page.locator('.client-row')).to_have_count(1)
            check('录入来源与查询客户端独立登记、独立 Key，权限不混用')

            nav(page, 'library')
            page.locator('.heading-actions [data-action="upload"]').click()
            page.locator('#material-type').select_option('article')
            page.locator('#material-title').fill('如何建立真正有效的学习系统')
            page.locator('#material-summary').fill('让信息不止于收藏，从问题、理解到行动，建立自己的知识循环。')
            page.locator('#article-content').fill('# 让信息流动起来\n\n先提出问题，再连接知识。\n\n<script>window.__injected=1</script>\n\n# 从理解走向行动\n\n给每一次学习一个足够小的行动。')
            creation_held = []
            creation_pattern = BASE + '/api/v1/admin/items'
            page.route(creation_pattern, lambda route: creation_held.append(route))
            page.locator('button[form="upload-form"]').click()
            assert creation_held
            page.locator('.dialog-footer [data-action="close-modal"]').click()
            expect(page.locator('.dialog')).to_have_count(1)
            expect(page.locator('.toast').last).to_contain_text('正在保存')
            creation_held[0].continue_()
            page.unroute(creation_pattern)
            expect(page.locator('#nav-count')).to_have_text('1')
            check('保存进度反馈与在途提交关闭保护')
            detail(page, '如何建立真正有效的学习系统')
            expect(page.locator('#detail-content')).to_contain_text('<script>window.__injected=1</script>')
            assert page.evaluate('window.__injected') is None
            expect(page.locator('#detail-content script')).to_have_count(0)
            approve(page)
            article = request('/admin/items?limit=10')['items'][0]
            assert request('/items/' + article['id'], key=READ)['title'] == '如何建立真正有效的学习系统'
            page.locator('.drawer-footer [data-action="edit"]').click()
            page.locator('#material-title').fill('如何建立真正有效的学习系统 · 修订版')
            page.locator('#article-content').fill('待审核的新正文，不应立即出现在客户端。')
            page.locator('button[form="upload-form"]').click()
            expect(page.locator('.drawer-scroll > .badge')).to_have_text('待审核')
            assert request('/items/' + article['id'], key=READ)['title'] == '如何建立真正有效的学习系统'
            assert request('/items/' + article['id'], key=READ)['content'].startswith('# 让信息')
            approve(page)
            page.keyboard.press('Escape')
            check('文章创建、XSS 安全文本预览、审核发布与修改前后公开快照隔离')

            for title, category, status in [('设计更好的日常：关于习惯的科学', '个人成长', 'draft'), ('信息过载时代，做一名清醒的读者', '认知思维', 'pending'), ('英语学习笔记：每天十五分钟', '语言学习', 'pending')]:
                request('/admin/items', 'POST', {'status': status, 'content': {'kind': 'article', 'title': title, 'summary': '把零散的信息，变成可以实践与复用的知识。', 'content': '从有意识的输入开始，用自己的语言表达，并在日常中检验它。', 'format': 'text', 'category': category, 'author': '知序管理员', 'tags': [category]}})
            client_ingested = request('/ingest/' + client['id'], 'POST', {'items': [{'external_id': 'desktop-note-001', 'kind': 'article', 'title': '来自桌面端的学习笔记', 'content': '通过来源专属密钥上传，不借用管理员权限。', 'category': '学习方法', 'author': '桌面学习端', 'tags': ['知识管理']}]}, key=source_key)
            check('管理员入库与客户端 JSON 接入使用真实且隔离的认证接口')

            page.reload()
            expect(page.locator('#nav-count')).to_have_text('5')
            audio = io.BytesIO()
            with wave.open(audio, 'wb') as w:
                w.setnchannels(1)
                w.setsampwidth(2)
                w.setframerate(8000)
                w.writeframes(b'\x00\x00' * 16000)
            page.locator('.heading-actions [data-action="upload"]').click()
            page.locator('#file-input').set_input_files({'name': '把时间当作朋友.wav', 'mimeType': 'audio/wav', 'buffer': audio.getvalue()})
            page.locator('#material-title').fill('把时间当作朋友：建立你的学习系统')
            page.locator('#material-category').select_option('个人成长')
            page.locator('#material-summary').fill('用长期主义的视角理解学习，让知识积累成为日常的习惯。')
            page.locator('button[form="upload-form"]').click()
            expect(page.locator('#nav-count')).to_have_text('6')
            detail(page, '把时间当作朋友')
            wait_js(page, "document.querySelector('audio')?.readyState >= 2")
            assert page.locator('audio').evaluate('a=>a.duration') > 1
            assert page.locator('audio').get_attribute('src').startswith('/api/v1/media/')
            page.locator('audio').evaluate('a=>a.play()')
            page.wait_for_timeout(100)
            page.locator('audio').evaluate('a=>a.pause()')
            approve(page)
            audio_item = request('/admin/items?kind=audio&limit=10')['items'][0]
            page.keyboard.press('Escape')
            check('音频真实上传、受认证媒体地址、浏览器解码与播放')

            ffmpeg = __import__('shutil').which('ffmpeg')
            if not ffmpeg:
                raise RuntimeError('FFmpeg is required only to generate the verification video fixture')
            recording = ARTIFACTS / 'test-recording.webm'
            subprocess.run([ffmpeg, '-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i', 'color=c=0xdce9c8:s=320x180:r=12', '-t', '1', '-an', '-c:v', 'libvpx', '-deadline', 'realtime', '-b:v', '100k', '-y', str(recording)], check=True, timeout=30, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            video_data = recording.read_bytes()
            assert len(video_data) > 200
            page.locator('.heading-actions [data-action="upload"]').click()
            page.locator('#file-input').set_input_files({'name': 'AI 通识课堂.webm', 'mimeType': 'video/webm', 'buffer': bytes(video_data)})
            page.locator('#material-title').fill('AI 通识课堂：理解模型的第一步')
            page.locator('#material-category').select_option('人工智能')
            page.locator('#material-summary').fill('从直观的图像出发，理解技术，也发现新的可能。')
            page.locator('button[form="upload-form"]').click()
            expect(page.locator('#nav-count')).to_have_text('7')
            detail(page, 'AI 通识课堂')
            wait_js(page, "document.querySelector('video')?.videoWidth === 320")
            page.locator('video').evaluate('v=>v.play()')
            wait_js(page, "document.querySelector('video')?.readyState >= 2")
            approve(page)
            page.keyboard.press('Escape')
            check('视频真实上传、WebM 浏览器解码与播放')

            png1 = make_png(page, '把复杂，变简单', '#eaf1dc')
            png2 = make_png(page, '让知识走向行动', '#ece7f1')
            page.locator('.heading-actions [data-action="upload"]').click()
            page.locator('#file-input').set_input_files([{'name': '思维卡片-01.png', 'mimeType': 'image/png', 'buffer': png1}, {'name': '思维卡片-02.png', 'mimeType': 'image/png', 'buffer': png2}])
            page.locator('#material-title').fill('认知思维卡片：把复杂变简单')
            page.locator('#material-category').select_option('认知思维')
            page.locator('#material-summary').fill('把复杂的思考工具装进口袋，从一个好问题开始。')
            page.locator('button[form="upload-form"]').click()
            expect(page.locator('#nav-count')).to_have_text('8')
            detail(page, '认知思维卡片')
            wait_js(page, "document.querySelector('.carousel-preview img')?.naturalWidth === 600")
            page.locator('[data-action="slide"][data-value="1"]').click()
            expect(page.locator('.carousel-preview img')).to_have_attribute('alt', '思维卡片-02.png，第 2 张卡片')
            approve(page)
            page.keyboard.press('Escape')
            check('多图片真实落盘、图片解码、轮播翻页和发布')

            page.locator('#type-filter').select_option('video')
            expect(page.locator('.materials-table tbody tr')).to_have_count(1)
            page.locator('#type-filter').select_option('all')
            page.locator('#library-search').fill('找不到的资料关键词')
            expect(page.locator('.empty h3')).to_have_text('还没有找到相关资料')
            page.locator('[data-action="reset-filters"]').click()
            expect(page.locator('.materials-table tbody tr')).to_have_count(6)
            page.get_by_role('button', name='第 2 页', exact=True).click()
            expect(page.locator('.materials-table tbody tr')).to_have_count(2)
            page.get_by_role('button', name='第 1 页', exact=True).click()
            expect(page.locator('.materials-table tbody tr')).to_have_count(6)
            page.locator('#select-all').check()
            expect(page.locator('.selection-number')).to_have_text('已选择 6 项')
            page.locator('[data-action="clear-selection"]').click()
            page.locator('[data-action="status"][data-value="pending"]').click()
            expect(page.locator('.materials-table tbody tr')).to_have_count(3)
            page.locator('[data-select]').first.check()
            page.locator('[data-action="bulk-publish"]').click()
            expect(page.locator('.materials-table tbody tr')).to_have_count(2)
            assert request('/admin/workspace')['summary']['pending'] == 2
            check('服务端搜索、类型筛选、总数分页、批量选择与审核')

            note_id = client_ingested['items'][0]['id']
            request('/ingest/' + client['id'], 'POST', {'items': [{'external_id': 'desktop-note-001', 'kind': 'article', 'title': '来自桌面端的学习笔记', 'content': '通过来源专属密钥上传，不借用管理员权限。', 'category': '客户端自定义分类', 'author': '桌面学习端', 'tags': ['知识管理', '原始标签']}]}, key=source_key)
            before_note = request('/admin/items/' + note_id)
            page.locator('[data-action="status"][data-value="pending"]').click()
            note_row = page.locator('.materials-table tbody tr').filter(has_text='来自桌面端的学习笔记')
            note_row.locator('.row-more').click()
            page.locator('.context-menu [data-action="edit"]').click()
            expect(page.locator('#material-category')).to_have_value('客户端自定义分类')
            page.locator('#material-summary').fill('管理员仅整理了摘要，不修改原作者、标签或客户端自定义分类。')
            page.locator('button[form="upload-form"]').click()
            expect(page.locator('.dialog')).to_have_count(0)
            saved_note = request('/admin/items/' + note_id)
            assert saved_note['draft']['author'] == before_note['draft']['author']
            assert saved_note['draft']['tags'] == before_note['draft']['tags']
            assert saved_note['draft']['category'] == '客户端自定义分类'
            assert saved_note['source_id'] == client['id']
            detail(page, '来自桌面端的学习笔记')
            approve(page)
            page.keyboard.press('Escape')
            assert request('/admin/workspace')['summary']['pending'] == 2
            check('编辑摘要保留原作者、原标签、来源和不在缓存中的自定义分类')

            nav(page, 'query')
            page.locator('#query-kind').select_option('video')
            page.locator('#query-category').fill('人工智能')
            page.locator('button[form="query-form"]').click()
            expect(page.locator('.query-table tbody tr')).to_have_count(1)
            page.locator('.query-result-row td').nth(1).click()
            expect(page.locator('[data-query-detail-state=ready]')).to_be_visible()
            wait_js(page, "document.querySelector('.public-query-detail video')?.readyState >= 2")
            assert page.locator('.public-query-detail video').evaluate('video=>video.duration') > 0
            expect(page.locator('.toast')).to_have_count(0, timeout=7000)
            page.screenshot(path=str(ARTIFACTS / 'query-detail-video-desktop.png'), full_page=True, animations='disabled')
            page.set_viewport_size({'width': 390, 'height': 920})
            wait_js(page, "document.querySelector('.sidebar').getBoundingClientRect().right <= 0.5")
            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
            page.screenshot(path=str(ARTIFACTS / 'query-detail-video-mobile.png'), full_page=True, animations='disabled')
            page.set_viewport_size({'width': 1600, 'height': 1100})
            close_modal(page)
            page.locator('#query-category').fill('')
            page.locator('#query-kind').select_option('audio')
            page.locator('button[form="query-form"]').click()
            expect(page.locator('.query-detail-button')).to_have_count(1)
            page.locator('.query-detail-button').click()
            expect(page.locator('[data-query-detail-state=ready]')).to_be_visible()
            wait_js(page, "document.querySelector('.public-query-detail audio')?.readyState >= 2")
            assert page.locator('.public-query-detail audio').evaluate('audio=>audio.duration') > 1
            close_modal(page)
            page.locator('#query-kind').select_option('carousel')
            page.locator('button[form="query-form"]').click()
            expect(page.locator('.query-title')).to_have_count(1)
            page.locator('.query-title').click()
            expect(page.locator('[data-query-detail-state=ready]')).to_be_visible()
            expect(page.locator('.public-query-detail .public-image')).to_have_count(2)
            wait_js(page, "[...document.querySelectorAll('.public-query-detail .public-image')].every(image=>image.complete&&image.naturalWidth>0)")
            close_modal(page)
            check('数据查询可打开视频/音频/轮播详情，公开媒体实际解码且手机弹窗无页面溢出')
            nav(page, 'receipts')
            expect(page.locator('.receipt-table')).to_be_visible()
            assert request('/admin/intakes?limit=100')['total'] >= 8
            check('公开查询过滤与真实入库回执内页，不再依赖专题或统计业务')

            expect(page.locator('.sidebar [data-route="push"]')).to_have_count(0)
            expect(page.locator('[data-action="bulk-push"]')).to_have_count(0)
            for path, method in [('/admin/plans', 'POST'), ('/admin/plans/old-plan/action', 'POST'), ('/clients/' + client['id'] + '/inbox', 'GET'), ('/stream', 'GET')]:
                response = context.request.fetch(BASE + '/api/v1' + path, method=method,
                    headers={'Authorization': 'Bearer ' + ADMIN}, data={} if method == 'POST' else None)
                try:
                    assert response.status == 404, (path, response.status)
                    assert response.json()['error']['code'] == 'not_found'
                finally:
                    response.dispose()
            candidates = request('/items?kind=audio&limit=20', key=query_key)['items']
            assert any(i['id'] == audio_item['id'] for i in candidates)
            published_changes = request('/changes?after=0&limit=100', key=query_key)
            assert any(c['item_id'] == audio_item['id'] for c in published_changes['changes'])
            assert 'plans' not in request('/admin/workspace')
            check('推送界面与计划/收件箱/SSE 接口已移除，客户端可主动筛选资料和拉取变更')

            nav(page, 'library')
            page.reload()
            expect(page.locator('h1')).to_have_text('数据录入')
            expect(page.locator('.client-pull-widget')).to_contain_text('推荐算法')
            expect(page.locator('.sidebar [data-route="push"]')).to_have_count(0)
            nav(page, 'sources')
            page.locator('.heading-actions [data-action="new-rss-source"]').click()
            page.locator('#source-name').fill('受限地址安全验证')
            page.locator('#source-url').fill('http://127.0.0.1:9099/private-feed')
            page.locator('button[form="source-form"]').click()
            expect(page.locator('#dialog-title')).to_have_text('RSS 订阅已添加')
            close_modal(page)
            rss_card = page.locator('.source-card').filter(has=page.locator('h3', has_text='受限地址安全验证'))
            rss_card.locator('[data-action="sync-source"]').click()
            expect(page.locator('.toast.error').last).to_contain_text('采集失败')
            rss_source = next(s for s in request('/admin/workspace')['sources'] if s['name'] == '受限地址安全验证')
            assert rss_source.get('last_error')
            rss_card.locator('[data-action="toggle-source"]').click()
            expect(rss_card.locator('[role="switch"]')).to_have_attribute('aria-checked', 'false')
            check('来源真实启停、RSS 失败反馈与内网地址抓取保护')

            nav(page, 'clients')
            page.locator('[data-action="client-access"]').click()
            page.locator('[data-action="rotate-client"]').click()
            page.locator('#confirm-operation').click()
            expect(page.locator('#one-time-key')).to_be_visible()
            old_query_key = query_key
            query_key = page.locator('#one-time-key').input_value()
            assert query_key != old_query_key
            close_modal(page)
            try:
                request('/items', key=old_query_key)
                raise AssertionError('Old key was accepted')
            except HTTPError as error:
                assert error.code == 401
            assert any(i['id'] == audio_item['id'] for i in request('/items?kind=audio', key=query_key)['items'])
            check('来源密钥真实轮换、旧密钥即时失效和权限延续')

            page.locator('.user-button').click()
            page.locator('#settings-name').fill('知序管理员')
            page.locator('#setting-compact').click()
            page.locator('button[form="settings-form"]').click()
            expect(page.locator('#profile-name')).to_have_text('知序管理员')
            assert page.locator('body').get_attribute('class').find('compact-table') >= 0
            page.reload()
            expect(page.locator('#profile-name')).to_have_text('知序管理员')
            page.locator('.user-button').click()
            page.locator('#setting-compact').click()
            page.locator('button[form="settings-form"]').click()
            nav(page, 'library')
            with page.expect_download() as download:
                page.locator('[data-action="export"]').click()
            download.value.save_as(ARTIFACTS / 'materials-export.csv')
            rows = list(csv.reader((ARTIFACTS / 'materials-export.csv').read_text(encoding='utf-8-sig').splitlines()))
            assert len(rows) == 9
            assert rows[0][0] == '标题'
            check('服务端设置、刷新后持久化、紧凑视图与真实 CSV 导出')

            for route in ['sources', 'clients', 'access', 'receipts', 'query', 'library']:
                nav(page, route)
                assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), route
            page.locator('[data-action="reset-filters"]').click() if page.locator('[data-action="reset-filters"]').count() else None
            # Explicitly reset the current category through the actual filter controls.
            page.locator('[data-action="status"][data-value="all"]').click()
            page.locator('#category-filter').select_option('all')
            expect(page.locator('.materials-table tbody tr')).to_have_count(6)
            expect(page.locator('.toast')).to_have_count(0, timeout=7000)
            page.screenshot(path=str(ARTIFACTS / 'management-desktop.png'), full_page=True)
            check('6 个真实导航页面与桌面截图')

            page.locator('.heading-actions [data-action="upload"]').click()
            page.locator('.dialog-header button').focus()
            page.keyboard.press('Shift+Tab')
            expect(page.locator('button[form="upload-form"]')).to_be_focused()
            page.keyboard.press('Tab')
            expect(page.locator('.dialog-header button')).to_be_focused()
            page.keyboard.press('Escape')
            check('对话框焦点约束、键盘导航与 Esc 关闭')
            for width in [1440, 1280, 1024, 768, 390]:
                page.set_viewport_size({'width': width, 'height': 920})
                assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), width
            page.locator('[data-action="open-nav"]').click()
            nav(page, 'sources')
            page.locator('[data-action="open-nav"]').click()
            nav(page, 'library')
            page.locator('[data-action="view"][data-value="grid"]').click()
            expect(page.locator('.material-card')).to_have_count(6)
            wait_js(page, "document.querySelector('.sidebar').getBoundingClientRect().right <= 0.5")
            page.screenshot(path=str(ARTIFACTS / 'management-mobile.png'), full_page=True)
            check('390–1600px 响应式、手机抽屉导航与实际资料卡片视图')

            stop()
            start(Path(temp), log, bootstrap=False)
            page.set_viewport_size({'width': 1600, 'height': 1100})
            page.goto(BASE + '/admin/?restart=1#library')
            login(page)
            expect(page.locator('#nav-count')).to_have_text('8')
            expect(page.locator('#profile-name')).to_have_text('知序管理员')
            restarted = request('/admin/workspace')
            assert 'plans' not in restarted
            assert 'collections' not in restarted and len(restarted['clients']) == 1
            assert any(i['id'] == audio_item['id'] for i in request('/items?kind=audio', key=query_key)['items'])
            page.locator('#type-filter').select_option('audio')
            expect(page.locator('.materials-table tbody tr')).to_have_count(1)
            detail(page, '把时间当作朋友')
            wait_js(page, "document.querySelector('audio')?.readyState >= 2")
            assert page.locator('audio').evaluate('a=>a.duration') > 1
            page.keyboard.press('Escape')
            assert page.evaluate('Object.keys(localStorage)') == []
            check('服务进程重启后资料、附件、设置、轮换密钥及客户端查询权限全部保留')

            held = []
            detail_pattern = BASE + '/api/v1/admin/items/' + audio_item['id']
            page.route(detail_pattern, lambda route: held.append(route))
            page.locator('.materials-table .material-name', has_text='把时间当作朋友').click()
            assert held
            nav(page, 'sources')
            held[0].fulfill(status=200, content_type='application/json', body=json.dumps({'data': request('/admin/items/' + audio_item['id'])}, ensure_ascii=False))
            page.wait_for_timeout(180)
            expect(page.locator('.drawer')).to_have_count(0)
            page.unroute(detail_pattern)
            check('切换页面后迟到的资料请求不会重新打开旧详情')

            nav(page, 'library')
            expired_pattern = '**/api/v1/admin/items?*'
            page.route(expired_pattern, lambda route: route.fulfill(status=401, content_type='text/html', body='<html>Proxy authentication required</html>'))
            page.locator('#library-search').fill('触发会话检查')
            expect(page.locator('#login-password')).to_be_visible()
            expect(page.locator('#login-error')).to_contain_text('会话已失效')
            page.unroute(expired_pattern)
            login(page)
            check('反向代理返回非 JSON 401 时仍能提示会话失效并安全重新登录')

            page.locator('.user-button').click()
            page.locator('[data-action="logout"]').click()
            expect(page.locator('#login-password')).to_be_visible()
            assert page.evaluate("document.cookie.includes('wzstudy_admin')") is False
            assert not ERRORS, ERRORS
            assert not EXTERNAL, EXTERNAL
            check('安全退出、无资料 localStorage 缓存、零外部网页请求与零未捕获 JS 异常')
            report = {'result': 'passed', 'version': '0.3.0', 'groups': len(CHECKS), 'checks': CHECKS, 'javascript_errors': ERRORS, 'external_requests': EXTERNAL, 'browser': browser.version, 'store': 'isolated real file store', 'viewport_widths': [1600, 1440, 1280, 1024, 768, 390], 'screenshots': ['management-desktop.png', 'management-mobile.png', 'query-detail-video-desktop.png', 'query-detail-video-mobile.png'], 'binary_sha256': hashlib.sha256((ARTIFACTS / 'wzstudy.exe').read_bytes()).hexdigest(), 'postgres_integration': 'not run by the isolated file-store browser suite'}
            (ARTIFACTS / 'live-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
            context.close()
            browser.close()
            print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
finally:
    stop()
