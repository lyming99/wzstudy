"""Browser regression for the standalone client-management page.

Builds a temporary executable and uses a disposable synthetic workspace; it
never reads live credentials or connects to a production instance.
"""
from pathlib import Path
import subprocess
import tempfile

from playwright.sync_api import expect, sync_playwright
import verify_two_chains as fixture


def main():
    with tempfile.TemporaryDirectory(prefix='client-navigation-') as tmp:
        root = Path(tmp)
        fixture.ARTIFACTS = root
        subprocess.run(['go', 'build', '-o', str(root / 'wzstudy.exe'), './cmd/api'],
                       cwd=fixture.ROOT, check=True)
        errors, external = [], []
        with (root / 'service.log').open('wb') as log:
            fixture.start(root / 'data', log)
            try:
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(channel='chrome', headless=True)
                    context = browser.new_context(viewport={'width': 1600, 'height': 1100},
                                                  locale='zh-CN', reduced_motion='reduce')
                    page = context.new_page()
                    page.set_default_timeout(12000)
                    page.on('pageerror', lambda error: errors.append(str(error)))
                    page.on('request', lambda request: external.append(request.url)
                            if request.url.startswith(('http://', 'https://'))
                            and not request.url.startswith(fixture.BASE + '/') else None)
                    page.goto(fixture.BASE + '/admin/#clients')
                    fixture.login(page)
                    expect(page.locator('h1')).to_have_text('客户端管理')
                    expect(page.locator('.section-tabs')).to_have_count(0)
                    fixture.verify_client_navigation(page)

                    fixture.nav(page, 'clients')
                    page.locator('[data-action=new-client]').click()
                    page.locator('#client-name').fill('导航测试客户端')
                    page.locator('#permission-preset').select_option('learner')
                    page.locator('button[form=client-form]').click()
                    expect(page.locator('#one-time-key')).to_be_visible()
                    assert page.locator('#one-time-key').input_value().startswith('wzck_')
                    fixture.close_modal(page)
                    row = page.locator('.client-row').filter(has_text='导航测试客户端')
                    expect(row).to_have_count(1)
                    expect(row).to_contain_text('读写个人学习数据')

                    row.locator('[data-action=edit-client]').click()
                    page.locator('#client-desc').fill('独立页面配置测试')
                    page.locator('button[form=client-form]').click()
                    expect(page.locator('.dialog')).to_have_count(0)
                    expect(row).to_contain_text('独立页面配置测试')

                    row.locator('[data-action=client-access]').click()
                    expect(page.locator('.dialog-body')).to_contain_text('GET /api/v1/items')
                    fixture.close_modal(page)
                    row.locator('[data-action=client-qr]').click()
                    expect(page.locator('#client-qr-panel')).to_be_visible()
                    fixture.close_modal(page)
                    row.locator('[data-action=client-feedback]').click()
                    expect(page.locator('.client-feedback-dialog')).to_contain_text('暂无反馈')
                    fixture.close_modal(page)
                    row.locator('[data-action=client-learning]').click()
                    expect(page.locator('.learning-state-dialog')).to_contain_text('暂无学习数据')
                    fixture.close_modal(page)

                    row.locator('[data-action=toggle-client]').click()
                    expect(row.locator('[data-action=toggle-client]')).to_have_text('启用')
                    row.locator('[data-action=toggle-client]').click()
                    expect(row.locator('[data-action=toggle-client]')).to_have_text('停用')
                    assert not errors, errors
                    assert not external, external
                    context.close()
                    browser.close()
                print('PASS: standalone client page retains creation, permissions, access, QR, feedback, learning and enable/disable', flush=True)
            finally:
                fixture.stop()


if __name__ == '__main__':
    main()
