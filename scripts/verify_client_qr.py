"""Real browser QR generation -> independent OpenCV decoding -> client import.
Only synthetic credentials and a disposable workspace; never uses live .local.
Requires the existing Playwright/OpenCV test runtime and .artifacts/wzstudy.exe.
"""
from pathlib import Path
import hashlib
import base64
import json
import sys
import tempfile
import time

import cv2
import numpy as np
from playwright.sync_api import sync_playwright, expect

import verify_two_chains as fixture
sys.path.insert(0, str(fixture.ROOT / 'examples'))
from client_connection import ConnectionRegistry, ConnectionError, parse_connection

ARTIFACTS, BASE = fixture.ARTIFACTS, fixture.BASE
CHECKS, ERRORS, EXTERNAL = [], [], []


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def decode_bytes(png):
    image = cv2.imdecode(np.frombuffer(png, np.uint8), cv2.IMREAD_COLOR)
    # Locator rasterization can add a pixel at a fractional CSS origin. The
    # ArUco and classic independent finders handle different finder patterns;
    # both decode the actual displayed pixels, not the encoder's input string.
    for constructor in (getattr(cv2, 'QRCodeDetectorAruco', cv2.QRCodeDetector), cv2.QRCodeDetector):
        text, _, _ = constructor().detectAndDecode(image)
        if text:
            return text
    raise AssertionError('Independent QR decoders did not recover UTF-8 configuration')


def decode_canvas(page):
    canvas = page.locator('#client-qr-canvas canvas')
    expect(canvas).to_be_visible()
    pixels = canvas.screenshot(animations='disabled')
    try:
        return decode_bytes(pixels)
    except AssertionError:
        (ARTIFACTS / 'client-qr-decode-failure.png').write_bytes(pixels)
        raw = base64.b64decode(canvas.evaluate("canvas=>canvas.toDataURL('image/png').split(',')[1]"))
        (ARTIFACTS / 'client-qr-decode-intrinsic.png').write_bytes(raw)
        print('QR_RENDER_DIAGNOSTIC:', canvas.evaluate("canvas=>({width:canvas.width,height:canvas.height,clientWidth:canvas.clientWidth,clientHeight:canvas.clientHeight,rect:{width:canvas.getBoundingClientRect().width,height:canvas.getBoundingClientRect().height}})"), flush=True)
        raise


def generate(page, address=BASE):
    if page.locator('#client-qr-url').input_value() != address:
        page.locator('#client-qr-url').fill(address)
    page.locator('#client-qr-confirm').check()
    page.locator('[data-action=generate-client-qr]').click()
    expect(page.locator('#client-qr-status')).to_contain_text('接入二维码已生成')
    return decode_canvas(page)


def open_existing(page, identifier, key):
    page.locator(f'.client-row [data-action=client-qr][data-id="{identifier}"]').click()
    expect(page.locator('#client-qr-key')).to_have_value('')
    page.locator('#client-qr-key').fill(key)


def set_policy(client, permissions, enabled=True):
    return fixture.api('/admin/clients/' + client['id'], 'PUT', {
        'slug': client['slug'], 'name': client['name'], 'description': client.get('description', ''),
        'enabled': enabled, 'permissions': permissions, 'revision': client['revision']})


def main():
    with tempfile.TemporaryDirectory(prefix='client-qr-', dir=ARTIFACTS) as temp, (ARTIFACTS / 'client-qr-service.log').open('wb') as log:
        fixture.start(Path(temp), log)
        try:
            with sync_playwright() as pw:
                browser = pw.chromium.launch(channel='chrome', headless=True)
                context = browser.new_context(viewport={'width': 1440, 'height': 1100}, locale='zh-CN', accept_downloads=True, reduced_motion='reduce')
                page = context.new_page()
                page.on('pageerror', lambda error: ERRORS.append(str(error)))
                requests = []
                def request_seen(request):
                    requests.append(request.url)
                    if request.url.startswith(('http://', 'https://')) and not request.url.startswith(BASE + '/'):
                        EXTERNAL.append(request.url)
                page.on('request', request_seen)
                page.add_init_script("""(() => {const active=new Set(),create=URL.createObjectURL.bind(URL),revoke=URL.revokeObjectURL.bind(URL);window.__qrBlobURLs=active;URL.createObjectURL=blob=>{const url=create(blob);active.add(url);return url};URL.revokeObjectURL=url=>{active.delete(url);revoke(url)}})()""")
                page.goto(BASE + '/admin/#clients')
                fixture.login(page)
                page.locator('[data-action=new-client]').click()
                page.locator('#client-name').fill('手机学习端😊')
                page.locator('button[form=client-form]').click()
                expect(page.locator('#one-time-key')).to_be_visible()
                key = page.locator('#one-time-key').input_value()
                initial_key_input = page.locator('#one-time-key').element_handle()
                client = fixture.api('/admin/clients')[0]
                before_revision = client['revision']
                expect(page.locator('#client-qr-panel')).to_be_visible()
                expect(page.locator('#client-qr-url-hint')).to_contain_text('手机自身')
                page.locator('[data-action=generate-client-qr]').click()
                expect(page.locator('#client-qr-status')).to_contain_text('请先确认')
                expect(page.locator('#client-qr-canvas canvas')).to_have_count(0)
                check('新建查询客户端提供扫码入口，回环地址提示明确，生成前需确认密钥与地址风险')

                for address in ['https://user:password@example.test', BASE + '/admin', BASE + '?key=bad', 'http://0.0.0.0:8091', 'http://example.test:0']:
                    page.locator('#client-qr-url').fill(address)
                    page.locator('#client-qr-confirm').check()
                    page.locator('[data-action=generate-client-qr]').click()
                    expect(page.locator('#client-qr-canvas canvas')).to_have_count(0)
                text = generate(page)
                config = parse_connection(text)
                assert config.access_key == key and config.client_id == client['id'] and config.name == '手机学习端😊'
                assert config.base_url == BASE
                assert fixture.api('/admin/clients/' + client['id'])['revision'] == before_revision
                assert key not in page.locator('#client-qr-result').inner_text()
                check('本地二维码可独立解码并保留中文/emoji、URL/ID/完整Key，生成不轮换或修改客户端')

                item = fixture.api('/admin/items', 'POST', {'status': 'pending', 'content': {'kind': 'article', 'title': 'QR query fixture', 'content': 'QR approved content'}}, expected=201)
                item = fixture.api('/admin/items/' + item['id'] + '/publish', 'POST', {'revision': item['revision']})
                registry = ConnectionRegistry()
                added = registry.add_from_qr(text, allow_insecure_http=True)
                assert added['added'] and added['verified'] and added['permissions'] == ['read', 'sync']
                assert not registry.add_from_qr(text, allow_insecure_http=True)['added']
                assert fixture.api('/items', key=config.access_key)['items'][0]['id'] == item['id']
                check('解码内容可直接导入客户端注册表，真实身份校验后查询资料，重复扫码幂等')

                with page.expect_download() as transfer:
                    page.locator('[data-action=download-client-qr]').click()
                downloaded = Path(temp) / 'qr-download.png'
                transfer.value.save_as(downloaded)
                assert parse_connection(decode_bytes(downloaded.read_bytes())).access_key == key
                old_canvas = page.locator('#client-qr-canvas canvas').element_handle()
                page.locator('#client-qr-url').fill('https://study.example.test')
                expect(page.locator('#client-qr-canvas canvas')).to_have_count(0)
                expect(page.locator('#client-qr-confirm')).not_to_be_checked()
                assert old_canvas.evaluate('(canvas)=>canvas.width===0&&!canvas.isConnected')
                remote_text = generate(page, 'https://study.example.test')
                assert parse_connection(remote_text).base_url == 'https://study.example.test'
                assert not EXTERNAL
                fixture.close_modal(page)
                assert page.evaluate('window.__qrBlobURLs.size') == 0
                assert initial_key_input.evaluate('(input)=>input.value===""&&!input.isConnected&&input.getAttribute("value")===null')
                expect(page.locator('[data-connection-key]')).to_have_count(0)
                check('下载PNG可扫描；改地址立即清除旧图并重新确认，不探测外站，关闭撤销图像和对象URL')

                source = fixture.api('/admin/sources', 'POST', {'slug': 'qr-collector', 'name': 'collector', 'permissions': ['ingest', 'upload']}, expected=201)
                other = fixture.api('/admin/clients', 'POST', {'slug': 'qr-other', 'name': 'other'}, expected=201)
                fixture.nav(page, 'clients')
                # The existing list is populated from the workspace, not a new credential response.
                page.reload()
                expect(page.locator(f'[data-action=client-qr][data-id="{client["id"]}"]')).to_be_visible()
                for bad_key in [fixture.ADMIN, fixture.READ, source['access_key'], other['access_key']]:
                    open_existing(page, client['id'], bad_key)
                    page.locator('#client-qr-confirm').check()
                    page.locator('[data-action=generate-client-qr]').click()
                    expect(page.locator('#client-qr-status')).to_have_class('field-note qr-error')
                    expect(page.locator('#client-qr-canvas canvas')).to_have_count(0)
                    expect(page.locator('#login-root')).to_be_empty()
                    fixture.close_modal(page)
                assert fixture.api('/admin/clients/' + client['id'])['revision'] == before_revision
                check('已有客户端不回读或静默轮换Key，管理员/全局/来源/其他客户端Key拒绝且不退出管理员会话')

                identity_url = BASE + '/api/v1/client/connection'
                page.route(identity_url, lambda route: route.fulfill(status=200, content_type='application/json', body=key + ' is not valid JSON'))
                open_existing(page, client['id'], key)
                page.locator('#client-qr-confirm').check()
                page.locator('[data-action=generate-client-qr]').click()
                expect(page.locator('#client-qr-status')).to_contain_text('无法解析')
                assert key not in page.locator('#client-qr-status').inner_text()
                fixture.close_modal(page)
                page.unroute(identity_url)

                open_existing(page, client['id'], key)
                assert parse_connection(generate(page)).access_key == key
                fixture.close_modal(page)
                open_existing(page, client['id'], key)
                page.locator('.dialog-footer [data-action=rotate-client]').click()
                assert fixture.api('/admin/clients/' + client['id'])['revision'] == before_revision
                page.locator('#confirm-operation').click()
                expect(page.locator('#one-time-key')).to_be_visible()
                new_key = page.locator('#one-time-key').input_value()
                assert new_key != key
                new_text = generate(page)
                assert parse_connection(new_text).access_key == new_key
                fixture.api('/client/connection', key=key, expected=401)
                try:
                    registry.add_from_qr(new_text, allow_insecure_http=True)
                    raise AssertionError('Unconfirmed Key update was accepted')
                except ConnectionError:
                    pass
                assert registry.add_from_qr(new_text, replace_existing=True, allow_insecure_http=True)['updated']
                fixture.api('/ingest/' + source['source']['id'] + '/status', key=source['access_key'])
                fixture.api('/client/connection', key=other['access_key'])
                fixture.close_modal(page)
                check('显式确认轮换后生成新二维码，旧Key立即失效，客户端更新须确认且其他身份不受影响')

                client = fixture.api('/admin/clients/' + client['id'])
                client = set_policy(client, ['sync'])
                open_existing(page, client['id'], new_key)
                sync_text = generate(page)
                assert ConnectionRegistry().add_from_qr(sync_text, allow_insecure_http=True)['permissions'] == ['sync']
                fixture.api('/items', key=new_key, expected=403)
                fixture.close_modal(page)
                client = set_policy(client, [])
                open_existing(page, client['id'], new_key)
                page.locator('#client-qr-confirm').check()
                page.locator('[data-action=generate-client-qr]').click()
                expect(page.locator('#client-qr-status')).to_contain_text('没有查询权限')
                expect(page.locator('#client-qr-canvas canvas')).to_have_count(0)
                fixture.close_modal(page)
                client = set_policy(client, ['read', 'sync'])
                check('仅同步权限可以配对但不会获得阅读，撤权后不能再生成或导入可用连接')

                held = []
                endpoint = BASE + '/api/v1/client/connection'
                page.route(endpoint, lambda route: held.append(route))
                open_existing(page, client['id'], new_key)
                page.locator('#client-qr-confirm').check()
                page.locator('[data-action=generate-client-qr]').click()
                expect(page.locator('#client-qr-status')).to_contain_text('正在验证')
                assert held
                page.keyboard.press('Escape')
                info = fixture.api('/client/connection', key=new_key)
                held.pop().fulfill(status=200, content_type='application/json', body=json.dumps({'data': info}))
                page.wait_for_timeout(150)
                expect(page.locator('#client-qr-panel')).to_have_count(0)
                open_existing(page, client['id'], new_key)
                page.locator('#client-qr-confirm').check()
                page.locator('[data-action=generate-client-qr]').click()
                expect(page.locator('#client-qr-status')).to_contain_text('正在验证')
                assert held
                page.locator('#client-qr-url').fill('https://changed.example.test')
                held.pop().fulfill(status=200, content_type='application/json', body=json.dumps({'data': info}))
                page.wait_for_timeout(150)
                expect(page.locator('#client-qr-canvas canvas')).to_have_count(0)
                fixture.close_modal(page)
                page.unroute(endpoint)
                check('关闭或改变输入后迟到的校验响应不会重建二维码或泄露旧配置')

                page.route(endpoint, lambda route: held.append(route))
                open_existing(page, client['id'], new_key)
                page.locator('#client-qr-confirm').check()
                page.locator('[data-action=generate-client-qr]').click()
                expect(page.locator('#client-qr-status')).to_contain_text('正在验证')
                assert held
                old_key_input = page.locator('#client-qr-key').element_handle()
                page.evaluate("() => showLogin('isolated QR session test')")
                fixture.login(page)
                held.pop().fulfill(status=200, content_type='application/json', body=json.dumps({'data': info}))
                page.wait_for_timeout(150)
                expect(page.locator('#client-qr-panel')).to_have_count(0)
                assert old_key_input.evaluate('(input)=>input.value===""&&!input.isConnected&&input.getAttribute("value")===null')
                page.unroute(endpoint)
                check('无效JSON不反显Key，失去会话后旧响应不展示二维码，旧输入值与HTML属性均被清除')

                open_existing(page, client['id'], new_key)
                final_text = generate(page)
                assert parse_connection(final_text).access_key == new_key
                expect(page.locator('.toast')).to_have_count(0, timeout=7000)
                page.screenshot(path=str(ARTIFACTS / 'client-qr-desktop.png'), full_page=True, animations='disabled')
                page.set_viewport_size({'width': 390, 'height': 920})
                assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
                assert parse_connection(decode_canvas(page)).access_key == new_key
                page.screenshot(path=str(ARTIFACTS / 'client-qr-mobile.png'), full_page=True, animations='disabled')
                page.keyboard.press('Escape')
                assert page.evaluate('Object.keys(localStorage).length+Object.keys(sessionStorage).length') == 0
                assert all(secret not in url for secret in [key, new_key, fixture.ADMIN, source['access_key']] for url in requests)
                assert not ERRORS and not EXTERNAL
                check('桌面和390px手机二维码均可解码，无浏览器持久凭据、URL密钥、外站请求或未捕获异常')
                context.close()
                browser.close()
                report = {'result': 'passed', 'groups': len(CHECKS), 'checks': CHECKS,
                          'independent_decoder': 'OpenCV ' + cv2.__version__, 'client_import': 'real identity endpoint and in-memory registry',
                          'browser_errors': ERRORS, 'external_requests': EXTERNAL,
                          'workspace': 'disposable synthetic file store; no live credentials or user materials',
                          'binary_sha256': hashlib.sha256((ARTIFACTS / 'wzstudy.exe').read_bytes()).hexdigest()}
                (ARTIFACTS / 'client-qr-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
                print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
        finally:
            fixture.stop()


if __name__ == '__main__':
    main()
