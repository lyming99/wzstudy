"""Verify saved query Key display and repeated QR access in an isolated service.
Build .artifacts/wzstudy.exe first. Uses synthetic credentials only.
"""
from pathlib import Path
import json
import tempfile

from playwright.sync_api import expect, sync_playwright

import verify_two_chains as fixture
import verify_client_qr as qr


def main():
    checks = []
    with tempfile.TemporaryDirectory(prefix='client-key-reuse-', dir=fixture.ARTIFACTS) as temp, (fixture.ARTIFACTS / 'client-key-reuse-service.log').open('wb') as log:
        fixture.start(Path(temp), log)
        try:
            with sync_playwright() as pw:
                browser = pw.chromium.launch(channel='chrome', headless=True)
                context = browser.new_context(viewport={'width': 1440, 'height': 1100}, permissions=['clipboard-read', 'clipboard-write'])
                page = context.new_page()
                errors = []
                page.on('pageerror', lambda error: errors.append(str(error)))
                page.goto(fixture.BASE + '/workspace/#clients')
                page.locator('#login-email').fill(fixture.EMAIL)
                page.locator('#login-password').fill(fixture.PASSWORD)
                page.locator('#login-form button[type=submit]').click()
                expect(page.locator('[data-action=new-client]')).to_be_visible()
                page.locator('[data-action=new-client]').click()
                page.locator('#client-name').fill('重复扫码测试客户端')
                page.locator('button[form=client-form]').click()
                key = page.locator('#one-time-key').input_value()
                assert len(key) >= 32
                fixture.close_modal(page)
                page.reload()
                button = page.locator('.client-row [data-action=client-qr]').first
                expect(button).to_be_visible()
                client_id = button.get_attribute('data-id')
                button.click()
                field = page.locator('#client-qr-key')
                expect(field).to_have_value(key)
                expect(field).to_have_attribute('type', 'password')
                page.locator('[data-action=reveal-qr-key]').click()
                expect(field).to_have_attribute('type', 'text')
                page.locator('[data-action=reveal-qr-key]').click()
                expect(field).to_have_attribute('type', 'password')
                page.locator('[data-action=copy-qr-key]').click()
                assert page.evaluate('navigator.clipboard.readText()') == key
                checks.append('刷新后自动回填原 Key，可显示、隐藏及复制')
                for attempt in range(2):
                    if attempt:
                        fixture.close_modal(page)
                        button.click()
                        expect(field).to_have_value(key)
                    payload = json.loads(qr.generate(page))
                    assert payload['access_key'] == key
                    assert payload['client_id'] == client_id
                    identity = fixture.api('/client/connection', key=payload['access_key'])
                    assert identity['client_id'] == client_id
                checks.append('两次打开弹窗生成的二维码均可解码，且使用同一有效 Key')
                page.set_viewport_size({'width': 390, 'height': 1000})
                assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                checks.append('390px 手机视口无横向溢出')
                handle = field.element_handle()
                fixture.close_modal(page)
                assert handle.evaluate('(input) => input.value') == ''
                assert not errors, errors
                checks.append('关闭弹窗清除 Key，浏览器无运行错误')
                browser.close()
        finally:
            fixture.stop()
    report = {'checks': checks, 'passed': len(checks)}
    (fixture.ARTIFACTS / 'client-key-reuse-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    for check in checks:
        print('PASS:', check)


if __name__ == '__main__':
    main()
